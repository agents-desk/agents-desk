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

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestTicketTypesHTTP 在完整 HTTP 路由上验证 TK1：工单类型与工单字段的权限检查、CRUD、排序、停用/启用和跨工作区隔离。
func TestTicketTypesHTTP(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, localDisk: env.Disk,
		channels: channel.DefaultRegistry(),
	}
	h := a.routes(nil)

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	if _, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "B主管理员", OwnerPassword: "password",
	}); err != nil {
		t.Fatal(err)
	}

	login := func(email string) *http.Cookie {
		t.Helper()
		r := call(h, "POST", "/api/auth/login", `{"email":"`+email+`","password":"password"}`, nil)
		if r.status != http.StatusNoContent {
			t.Fatalf("登录 %s：%d %s", email, r.status, r.body)
		}
		return r.cookie
	}
	idOf := func(body string) string {
		var v struct{ ID string }
		_ = json.Unmarshal([]byte(body), &v)
		return v.ID
	}

	owner, betaOwner := login("owner@alpha.com"), login("owner@beta.com")
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"坐席","password":"password","role_id":"`+agentRole+`"}`, owner)
	agent := login("agent@alpha.com")

	// 坐席无 ticket-types:manage，建类型应 403
	if r := call(h, "POST", "/t/alpha/api/ticket-types", `{"name":"无权限"}`, agent); r.status != http.StatusForbidden {
		t.Fatalf("坐席新建工单类型应 403：%d %s", r.status, r.body)
	}

	// 主管理员新建两个工单类型
	r := call(h, "POST", "/t/alpha/api/ticket-types", `{"name":"充值问题","description":"掉单","default_visibility":"all"}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建工单类型：%d %s", r.status, r.body)
	}
	type1 := idOf(r.body)
	r = call(h, "POST", "/t/alpha/api/ticket-types", `{"name":"内部审批","default_visibility":"members"}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建第二个工单类型：%d %s", r.status, r.body)
	}
	type2 := idOf(r.body)
	betaType := idOf(call(h, "POST", "/t/beta/api/ticket-types", `{"name":"B类型"}`, betaOwner).body)

	// 坐席能读取工单类型列表；跨工作区读取应 404
	if r := call(h, "GET", "/t/alpha/api/ticket-types", "", agent); r.status != http.StatusOK || !strings.Contains(r.body, type1) {
		t.Fatalf("坐席读取工单类型列表：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/ticket-types/"+betaType, "", owner); r.code != "ticket_type.not_found" {
		t.Fatalf("跨工作区读取应 404：%d %s", r.status, r.body)
	}

	// 工单属性接口权限检查：坐席不能建工单属性，主管理员可以建和排序
	if r := call(h, "POST", "/t/alpha/api/attributes", `{"target":"ticket","ticket_type_id":"`+type1+`","key":"order_no","name":"订单号","type":"text"}`, agent); r.status != http.StatusForbidden {
		t.Fatalf("坐席新建工单属性应 403：%d %s", r.status, r.body)
	}
	r = call(h, "POST", "/t/alpha/api/attributes", `{"target":"ticket","ticket_type_id":"`+type1+`","key":"order_no","name":"订单号","type":"text"}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建工单属性：%d %s", r.status, r.body)
	}
	f1 := idOf(r.body)
	r = call(h, "POST", "/t/alpha/api/attributes", `{"target":"ticket","ticket_type_id":"`+type1+`","key":"amount","name":"金额","type":"number"}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建第二个工单属性：%d %s", r.status, r.body)
	}
	f2 := idOf(r.body)

	// 按工单类型过滤和排序字段
	if r := call(h, "PUT", "/t/alpha/api/attributes/order", `{"target":"ticket","ticket_type_id":"`+type1+`","ids":["`+f2+`","`+f1+`"]}`, owner); r.status != http.StatusOK || strings.Index(r.body, f2) > strings.Index(r.body, f1) {
		t.Fatalf("工单字段排序：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/attributes?target=ticket&ticket_type_id="+type1, "", agent); r.status != http.StatusOK || !strings.Contains(r.body, "order_no") {
		t.Fatalf("读取工单字段列表：%d %s", r.status, r.body)
	}

	// 停用工单类型后 enabled_only 过滤生效
	if r := call(h, "POST", "/t/alpha/api/ticket-types/"+type2+"/disable", "", owner); r.status != http.StatusOK || !strings.Contains(r.body, `"disabled"`) {
		t.Fatalf("停用工单类型：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/ticket-types?enabled_only=true", "", agent); r.status != http.StatusOK || strings.Contains(r.body, type2) {
		t.Fatalf("enabled_only 应排除停用类型：%d %s", r.status, r.body)
	}
}
