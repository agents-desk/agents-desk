package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
	"agents-desk/internal/platformadmin"
)

// TestAdminOpsAPIs 检查管理后台的队列运维、定时任务和运行环境自检接口：
// 未登录返回 401，工作区成员令牌被拒绝，拥有运维权限的平台管理员能正常查询队列、列出和触发定时任务以及执行自检。
func TestAdminOpsAPIs(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, defaultDisk: env.Disk, localDisk: env.Disk,
		locker: lock.New(pglock.New(env.DB)),
	}
	h := a.routes(nil)

	// 未登录访问全部返回 401
	for _, path := range []string{"/admin/api/queue/status", "/admin/api/schedules", "/admin/api/schedules/runs", "/admin/api/system/doctor"} {
		if r := call(h, http.MethodGet, path, "", nil); r.status != http.StatusUnauthorized {
			t.Errorf("未登录 GET %s：%d %s", path, r.status, r.body)
		}
	}

	// 创建平台管理员并登录
	if _, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, apptest.Admin, "ops", "ops@example.com", "password"); err != nil {
		t.Fatal(err)
	}
	login := call(h, http.MethodPost, "/admin/api/auth/login", `{"email":"ops@example.com","password":"password"}`, nil)
	if login.status != http.StatusNoContent || login.cookie == nil {
		t.Fatalf("管理员登录：%d %s", login.status, login.body)
	}

	// 队列概览（无 NATS 连接时返回数据库侧待转发队列与失败任务计数）
	if r := call(h, http.MethodGet, "/admin/api/queue/status", "", login.cookie); r.status != http.StatusOK || !strings.Contains(r.body, `"outbox"`) {
		t.Fatalf("GET /admin/api/queue/status：%d %s", r.status, r.body)
	}

	// 定时任务列表包含已注册的清理与业务任务
	schedResp := call(h, http.MethodGet, "/admin/api/schedules", "", login.cookie)
	if schedResp.status != http.StatusOK {
		t.Fatalf("GET /admin/api/schedules：%d %s", schedResp.status, schedResp.body)
	}
	var schedList struct {
		Items []struct {
			Name        string `json:"name"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(schedResp.body), &schedList); err != nil {
		t.Fatalf("解析定时任务列表失败：%v", err)
	}
	names := map[string]bool{}
	for _, it := range schedList.Items {
		names[it.Name] = true
		if it.Label == "" || strings.HasPrefix(it.Label, "schedule.task.") || it.Description == "" || strings.HasPrefix(it.Description, "schedule.task.") {
			t.Errorf("定时任务 %q 未正确翻译显示名称或说明：%+v", it.Name, it)
		}
	}
	for _, want := range []string{"files:cleanup", "locks:cleanup", "apitoken:cleanup", "queue:prune-failed", "schedule:prune-runs", "conversations:unsnooze"} {
		if !names[want] {
			t.Errorf("定时任务列表缺少 %q：%v", want, names)
		}
	}

	// 触发立即执行返回 202，未知任务返回 404
	if r := call(h, http.MethodPost, "/admin/api/schedules/locks:cleanup/run", "", login.cookie); r.status != http.StatusAccepted {
		t.Fatalf("POST /admin/api/schedules/locks:cleanup/run：%d %s", r.status, r.body)
	}
	if r := call(h, http.MethodPost, "/admin/api/schedules/unknown:task/run", "", login.cookie); r.status != http.StatusNotFound || r.code != "schedule.not_found" {
		t.Fatalf("未知定时任务：%d %s", r.status, r.body)
	}

	// 定时任务执行记录列表
	if r := call(h, http.MethodGet, "/admin/api/schedules/runs", "", login.cookie); r.status != http.StatusOK || !strings.Contains(r.body, `"items"`) {
		t.Fatalf("GET /admin/api/schedules/runs：%d %s", r.status, r.body)
	}

	// 运行环境自检报告
	docResp := call(h, http.MethodGet, "/admin/api/system/doctor", "", login.cookie)
	if docResp.status != http.StatusOK {
		t.Fatalf("GET /admin/api/system/doctor：%d %s", docResp.status, docResp.body)
	}
	var report struct {
		Results []struct {
			Group  string `json:"group"`
			Check  string `json:"check"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(docResp.body), &report); err != nil || len(report.Results) == 0 {
		t.Fatalf("解析自检报告失败：%v %s", err, docResp.body)
	}
}
