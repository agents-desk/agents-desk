package tenant_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tenant"
)

// TestImpersonateEndToEnd 覆盖管理员代为登录工作区成员的完整链路：
// 1. 列出可代登成员并签发锁定在当前工作区的代登 cookie（__Host-impersonation）；
// 2. 账号自省接口 /api/me 返回操作者信息，/api/tenants 只返回锁定的目标工作区；
// 3. 目标工作区接口可访问，同一成员所属的另一个工作区返回 403；
// 4. 只读模式下写接口返回 403；
// 5. 敏感接口（修改密码）返回 403 auth.impersonation_forbidden；
// 6. 退出代登只清除 __Host-impersonation，不破坏原会话；
// 7. 停用发起代登的平台管理员后，已签发的代登令牌立即失效（401）。
func TestImpersonateEndToEnd(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	system := identity.Subject{Type: identity.System, ID: "system"}

	// 创建两位平台管理员（operator 发起代登，supervisor 负责停用 operator）。
	supervisor, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, system, "超级管理员", "super@example.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, system, "值班运营", "ops@example.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}

	// 创建两个工作区，同一个主管理员 alice 同时属于 acme 和 globex。
	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, addr)
	acme, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Acme", Slug: "acme", OwnerEmail: "alice@example.com", OwnerName: "Alice", OwnerPassword: "secret-pass-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Globex", Slug: "globex", OwnerEmail: "alice@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	disabledTenant, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Closed", Slug: "closed", OwnerEmail: "alice@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.NewDisable(env.DB, env.MemberLogins, addr).Handle(ctx, apptest.Admin, disabledTenant.ID); err != nil {
		t.Fatal(err)
	}

	adminTokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour})
	memberTokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour})
	opsToken, err := adminTokens.Login(ctx, identity.Subject{Type: platformadmin.Type, ID: operator.ID}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}

	// 装配与 cmd/agents-desk/http.go 一致的三组 API：/admin/api、/api、/t/{slug}/api。
	router := chi.NewRouter()
	impersonation := &identity.Impersonation{
		Actors:      identity.Accounts{platformadmin.Type: platformadmin.Resolve(env.DB, env.Roles)},
		Permissions: []string{string(auth.TenantsImpersonate)},
	}
	router.Route("/admin/api", func(r chi.Router) {
		api := rest.New(r, rest.Options{
			BasePath:      "/admin/api",
			Accounts:      identity.Accounts{platformadmin.Type: platformadmin.Resolve(env.DB, env.Roles)},
			SessionCookie: platformadmin.SessionCookie,
		})
		require := func(p auth.Permission) func(*huma.Operation) {
			return rest.RequireToken(api, adminTokens.Validate, string(p))
		}
		tenant.RegisterRoutes(api, require(auth.TenantsRead), require(auth.TenantsManage), require(auth.TenantsImpersonate), env.DB, env.Roles, env.MemberLogins, memberTokens, addr, member.Hooks{})
	})
	router.Route("/api", func(r chi.Router) {
		api := rest.New(r, rest.Options{
			BasePath:      "/api",
			Accounts:      identity.Accounts{member.Type: member.Resolve(env.DB, env.Roles)},
			Impersonation: impersonation,
			AccountScope:  true,
			SessionCookie: member.SessionCookie,
		})
		requireMember := rest.RequireToken(api, memberTokens.Validate)
		member.RegisterRoutes(api, requireMember, env.DB, env.MemberLogins, memberTokens, env.Files, 2<<20, time.Hour)
		tenant.RegisterMemberRoutes(api, requireMember, env.DB, env.Roles, env.Files, addr)
	})
	router.Route("/t/{slug}/api", func(r chi.Router) {
		r.Use(tenant.Middleware(env.DB))
		api := rest.New(r, rest.Options{
			BasePath:      "/t/{slug}/api",
			Tenant:        tenant.FromRequest,
			Accounts:      identity.Accounts{member.Type: tenant.Resolve(member.Resolve(env.DB, env.Roles))},
			Impersonation: impersonation,
			SessionCookie: member.SessionCookie,
		})
		require := func(perms ...auth.Permission) func(*huma.Operation) {
			names := make([]string, len(perms))
			for i, p := range perms {
				names[i] = string(p)
			}
			return rest.RequireToken(api, memberTokens.Validate, names...)
		}
		tenant.RegisterTenantRoutes(api, require, env.DB, env.Files)
		member.RegisterTenantRoutes(api, require, env.DB, env.Roles, env.MemberLogins, env.Files, member.Hooks{})
	})

	do := func(method, path, body, bearer string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		if len(cookies) > 0 && method != http.MethodGet {
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	// 1. 列出可代登成员，主管理员排首位。
	rr := do(http.MethodGet, "/admin/api/tenants/"+acme.ID+"/members", "", opsToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("list members: %d %s", rr.Code, rr.Body.String())
	}
	var members []tenant.ImpersonateMemberDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &members); err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].ID != acme.OwnerID || !members[0].Owner {
		t.Fatalf("unexpected members: %+v", members)
	}

	// 2. 已停用的工作区不允许代登录；不存在的成员不允许代登录。
	if w := do(http.MethodPost, "/admin/api/tenants/"+disabledTenant.ID+"/impersonate", `{"reason":"排查"}`, opsToken); w.Code != http.StatusForbidden {
		t.Fatalf("disabled tenant expected 403, got %d %s", w.Code, w.Body.String())
	}
	if _, err := tenant.NewImpersonate(env.DB, env.Roles, memberTokens).Handle(ctx, identity.Subject{Type: platformadmin.Type, ID: operator.ID}, acme.ID, tenant.ImpersonateRequest{
		MemberID: "01900000-0000-7000-8000-0000000000ff",
		Reason:   "排查",
	}, apitoken.Device{}); !errors.Is(err, member.ErrNotFound) {
		t.Fatalf("non-member expected member.ErrNotFound, got %v", err)
	}

	// 3. 发起只读代登录（默认代登主管理员 alice），返回独立的 __Host-impersonation cookie。
	rr = do(http.MethodPost, "/admin/api/tenants/"+acme.ID+"/impersonate", `{"reason":"工单 #1024","read_only":true}`, opsToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("impersonate: %d %s", rr.Code, rr.Body.String())
	}
	var impCookie *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == "__Host-impersonation" {
			impCookie = ck
		}
	}
	if impCookie == nil || impCookie.Value == "" {
		t.Fatalf("expected __Host-impersonation cookie, got %+v", rr.Result().Cookies())
	}

	// 4. 账号自省 /api/me 返回操作者与只读标识；/api/tenants 只返回锁定的 acme（不暴露 globex）。
	rr = do(http.MethodGet, "/api/me", "", "", impCookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rr.Code, rr.Body.String())
	}
	var profile member.Profile
	if err := json.Unmarshal(rr.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.ID != acme.OwnerID || profile.Impersonation == nil || profile.Impersonation.ActorID != operator.ID || profile.Impersonation.ActorName != "值班运营" || !profile.Impersonation.ReadOnly {
		t.Fatalf("unexpected profile: %+v", profile)
	}

	rr = do(http.MethodGet, "/api/tenants", "", "", impCookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("mine: %d %s", rr.Code, rr.Body.String())
	}
	var mine []tenant.MineItem
	if err := json.Unmarshal(rr.Body.Bytes(), &mine); err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].Slug != "acme" {
		t.Fatalf("expected only bound tenant acme, got %+v", mine)
	}

	// 5. 访问目标工作区 acme 成功，访问另一工作区 globex 返回 403；只读模式下修改工作区名称返回 403。
	if w := do(http.MethodGet, "/t/acme/api/tenant", "", "", impCookie); w.Code != http.StatusOK {
		t.Fatalf("acme current: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodGet, "/t/globex/api/tenant", "", "", impCookie); w.Code != http.StatusForbidden {
		t.Fatalf("cross tenant expected 403, got %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPut, "/t/acme/api/tenant", `{"name":"Hacked"}`, "", impCookie); w.Code != http.StatusForbidden {
		t.Fatalf("read-only rename expected 403, got %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPut, "/t/acme/api/me/status", `{"status":"online"}`, "", impCookie); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"auth.read_only"`) {
		t.Fatalf("read-only set status expected 403 auth.read_only, got %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPost, "/api/files", ``, "", impCookie); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"auth.read_only"`) {
		t.Fatalf("read-only file upload expected 403 auth.read_only, got %d %s", w.Code, w.Body.String())
	}

	// 6. 敏感路由（DirectOnly）直接返回 403 auth.impersonation_forbidden。
	if w := do(http.MethodPut, "/api/me/password", `{"current_password":"secret-pass-123","new_password":"new-secret-pass-456"}`, "", impCookie); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"auth.impersonation_forbidden"`) {
		t.Fatalf("direct-only password change: %d %s", w.Code, w.Body.String())
	}

	// 7. 退出代登录仅清除 __Host-impersonation，不清除 __Host-session。
	rr = do(http.MethodPost, "/api/auth/logout", "", "", impCookie)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("logout impersonation: %d %s", rr.Code, rr.Body.String())
	}
	var clearedImp bool
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == "__Host-impersonation" && ck.MaxAge < 0 {
			clearedImp = true
		}
		if ck.Name == "__Host-session" {
			t.Fatalf("logout impersonation should not touch __Host-session: %+v", ck)
		}
	}
	if !clearedImp {
		t.Fatalf("expected __Host-impersonation to be cleared, got %+v", rr.Result().Cookies())
	}

	// 8. 再签发一次代登令牌，停用平台管理员后该代登令牌立即失效（401）。
	rr = do(http.MethodPost, "/admin/api/tenants/"+acme.ID+"/impersonate", `{"reason":"工单 #1025"}`, opsToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("second impersonate: %d %s", rr.Code, rr.Body.String())
	}
	var secondImp *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == "__Host-impersonation" {
			secondImp = ck
		}
	}
	if _, err := platformadmin.NewDisable(env.DB, env.AdminLogins).Handle(ctx, identity.Subject{Type: platformadmin.Type, ID: supervisor.ID}, operator.ID); err != nil {
		t.Fatal(err)
	}
	if w := do(http.MethodGet, "/t/acme/api/tenant", "", "", secondImp); w.Code != http.StatusUnauthorized {
		t.Fatalf("disabled admin impersonation expected 401, got %d %s", w.Code, w.Body.String())
	}
}
