package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestReportsAPI 检查 6 个报表接口的认证、reports:read 权限控制与基本响应。
func TestReportsAPI(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
	}
	h := a.routes(nil)

	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	if _, err := member.NewAdd(env.DB, env.MemberLogins, env.Files).Handle(ctx, sys, member.AddParams{
		Email: "agent@alpha.com", Name: "坐席", Password: "password", RoleID: agentRole.ID,
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
	ownerCookie := login("owner@alpha.com")
	agentCookie := login("agent@alpha.com")

	endpoints := []string{
		"/t/alpha/api/reports/conversations",
		"/t/alpha/api/reports/response-time",
		"/t/alpha/api/reports/satisfaction",
		"/t/alpha/api/reports/satisfaction/ratings",
		"/t/alpha/api/reports/members",
		"/t/alpha/api/reports/translation",
	}

	for _, ep := range endpoints {
		// 坐席默认没有 reports:read，返回 403
		if r := call(h, "GET", ep, "", agentCookie); r.status != http.StatusForbidden {
			t.Errorf("agent GET %s: status=%d, want 403 (%s)", ep, r.status, r.body)
		}
		// 主管理员拥有 reports:read，返回 200
		if r := call(h, "GET", ep+"?from=2026-11-01&to=2026-11-07&tz=Asia/Shanghai", "", ownerCookie); r.status != http.StatusOK {
			t.Errorf("owner GET %s: status=%d, want 200 (%s)", ep, r.status, r.body)
		}
	}

	// 非法日期参数返回 422
	if r := call(h, "GET", "/t/alpha/api/reports/conversations?from=2026-11-10&to=2026-11-01", "", ownerCookie); r.status != http.StatusUnprocessableEntity {
		t.Errorf("invalid range status=%d, want 422 (%s)", r.status, r.body)
	}
	// 普通坐席可以访问只读渠道列表（channels:read 在基本权限中）
	if r := call(h, "GET", "/t/alpha/api/channels", "", agentCookie); r.status != http.StatusOK {
		t.Errorf("agent GET /channels: status=%d, want 200 (%s)", r.status, r.body)
	}
}
