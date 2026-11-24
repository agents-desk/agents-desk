package platformadmin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
)

// newAdminAPI 按入口的方式装配管理员端的 API：只接受平台管理员，会话 cookie 是 __Host-admin。
func newAdminAPI(env *apptest.Env, tokens *apitoken.Store) http.Handler {
	api := rest.New(chi.NewRouter(), rest.Options{
		Accounts:      identity.Accounts{platformadmin.Type: platformadmin.Resolve(env.DB, env.Roles)},
		SessionCookie: platformadmin.SessionCookie,
	})
	platformadmin.RegisterRoutes(api, rest.RequireToken(api, tokens.Validate), env.DB, env.AdminLogins, tokens, env.Files, 20*filesystem.MiB, time.Hour)
	return api.Handler()
}

func TestLoginSetsAdminCookieAndMeReturnsPermissions(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	if _, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, system, "运维", "ops@example.com", "secret-pass-123"); err != nil {
		t.Fatal(err)
	}
	h := newAdminAPI(env, apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour}))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"ops@example.com","password":"secret-pass-123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("登录：status=%d body=%s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-admin" || !cookies[0].HttpOnly {
		t.Fatalf("登录写入的 cookie：%v", cookies)
	}

	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(cookies[0])
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var me platformadmin.Profile
	if err := json.Unmarshal(rr.Body.Bytes(), &me); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("me：status=%d body=%s", rr.Code, rr.Body.String())
	}
	if me.Email != "ops@example.com" || len(me.Permissions) != len(auth.PlatformAdmin.Permissions) {
		t.Fatalf("me：%+v", me)
	}
}

func TestAdminAPIRejectsMemberTokensAndDisabledAdmins(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	admin, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, system, "运维", "ops@example.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour})
	h := newAdminAPI(env, tokens)
	me := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	// 成员的 token 不被管理员端接受
	memberToken, err := tokens.Login(ctx, identity.Subject{Type: member.Type, ID: "01900000-0000-7000-8000-0000000000bb"}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	if code := me(memberToken); code != http.StatusUnauthorized {
		t.Fatalf("成员的 token：status=%d", code)
	}

	// 停用之后，已经签发的 token 立即失效
	adminToken, err := tokens.Login(ctx, identity.Subject{Type: platformadmin.Type, ID: admin.ID}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	if code := me(adminToken); code != http.StatusOK {
		t.Fatalf("停用前：status=%d", code)
	}
	if _, err := env.DB.NewRaw("UPDATE platform_admins SET disabled_at = ? WHERE id = ?", time.Now().UTC(), admin.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if code := me(adminToken); code != http.StatusUnauthorized {
		t.Fatalf("停用后：status=%d", code)
	}
}
