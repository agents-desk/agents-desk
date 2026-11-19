package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tenant"
)

// response 是一次请求的结果：状态码、错误码和响应体。
type response struct {
	status int
	code   string
	body   string
	cookie *http.Cookie
}

// call 向 h 发送请求，cookie 非空时带上它。
func call(h http.Handler, method, path, body string, cookie *http.Cookie) response {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &problem)
	r := response{status: rec.Code, code: problem.Code, body: rec.Body.String()}
	if cookies := rec.Result().Cookies(); len(cookies) > 0 {
		r.cookie = cookies[0]
	}
	return r
}

// TestMemberAPIs 在完整的路由上检查成员登录、所属的工作区，以及按 slug 进入工作区：
// 不是成员和 slug 不存在得到同样的 404，停用的工作区得到 403，平台管理员的登录凭据不被接受。
func TestMemberAPIs(t *testing.T) {
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

	// 工作区：demo 和 off 的主管理员是 owner，other 的是另一个人；off 已停用
	create := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{PublicURL: "http://localhost:8081"})
	ids := map[string]string{}
	for _, c := range []struct{ slug, name, owner string }{{"demo", "B 示例", "owner"}, {"off", "A 已停用", "owner"}, {"other", "C 别人的", "other"}} {
		created, err := create.Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: c.name, Slug: c.slug, OwnerEmail: c.owner + "@example.com", OwnerName: c.owner, OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[c.slug] = created.ID
	}
	if _, err := tenant.NewDisable(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, ids["off"]); err != nil {
		t.Fatal(err)
	}

	// 登录
	if r := call(h, "POST", "/api/auth/login", `{"email":"owner@example.com","password":"wrong-password"}`, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("密码错误：%d %s", r.status, r.body)
	}
	login := call(h, "POST", "/api/auth/login", `{"email":"owner@example.com","password":"password"}`, nil)
	if login.status != http.StatusNoContent || login.cookie == nil || login.cookie.Name != "__Host-session" {
		t.Fatalf("登录：%d %s %v", login.status, login.body, login.cookie)
	}
	session := login.cookie

	if r := call(h, "GET", "/api/me", "", session); r.status != http.StatusOK || !strings.Contains(r.body, `"email":"owner@example.com"`) {
		t.Fatalf("/api/me：%d %s", r.status, r.body)
	}
	// 所属的工作区按名称排序，停用的也列出
	r := call(h, "GET", "/api/tenants", "", session)
	var mine []tenant.MineItem
	if err := json.Unmarshal([]byte(r.body), &mine); err != nil || r.status != http.StatusOK {
		t.Fatalf("/api/tenants：%d %s", r.status, r.body)
	}
	if len(mine) != 2 || mine[0].Slug != "off" || mine[0].Status != tenant.Disabled || mine[1].Slug != "demo" || mine[1].URL != "http://localhost:8081/t/demo/" {
		t.Fatalf("/api/tenants：%+v", mine)
	}

	// 进入工作区
	r = call(h, "GET", "/t/demo/api/tenant", "", session)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"id":"`+ids["demo"]+`"`) || !strings.Contains(r.body, `"permissions":["*"]`) {
		t.Fatalf("进入 demo：%d %s", r.status, r.body)
	}
	other := call(h, "GET", "/t/other/api/tenant", "", session)
	missing := call(h, "GET", "/t/missing/api/tenant", "", session)
	if other.status != http.StatusNotFound || missing.status != other.status || missing.code != other.code || missing.body != other.body {
		t.Fatalf("不是成员与不存在应当相同：\n%d %s\n%d %s", other.status, other.body, missing.status, missing.body)
	}
	if r := call(h, "GET", "/t/off/api/tenant", "", session); r.status != http.StatusForbidden || r.code != "tenant.disabled" {
		t.Fatalf("停用的工作区：%d %s", r.status, r.body)
	}
	// 别人看停用的工作区仍是 404
	otherLogin := call(h, "POST", "/api/auth/login", `{"email":"other@example.com","password":"password"}`, nil)
	if r := call(h, "GET", "/t/off/api/tenant", "", otherLogin.cookie); r.status != http.StatusNotFound {
		t.Fatalf("别人看停用的工作区：%d %s", r.status, r.body)
	}

	// 平台管理员的登录凭据：cookie 名称不同，换成成员的 cookie 名称也不被接受
	if _, err := platformadmin.NewCreate(env.DB, env.AdminLogins).Handle(ctx, apptest.Admin, "ops", "ops@example.com", "password"); err != nil {
		t.Fatal(err)
	}
	admin := call(h, "POST", "/admin/api/auth/login", `{"email":"ops@example.com","password":"password"}`, nil)
	if admin.status != http.StatusNoContent {
		t.Fatalf("管理员登录：%d %s", admin.status, admin.body)
	}
	disguised := &http.Cookie{Name: session.Name, Value: admin.cookie.Value}
	for _, path := range []string{"/api/me", "/t/demo/api/tenant"} {
		if r := call(h, "GET", path, "", admin.cookie); r.status != http.StatusUnauthorized {
			t.Errorf("%s 带管理员的 cookie：%d %s", path, r.status, r.body)
		}
		if r := call(h, "GET", path, "", disguised); r.status != http.StatusUnauthorized {
			t.Errorf("%s 带管理员的 token：%d %s", path, r.status, r.body)
		}
	}
	// 成员的 cookie 也进不了管理员端
	if r := call(h, "GET", "/admin/api/me", "", &http.Cookie{Name: admin.cookie.Name, Value: session.Value}); r.status != http.StatusUnauthorized {
		t.Errorf("管理员端带成员的 token：%d %s", r.status, r.body)
	}

	// 账号停用后，已经登录的会话立即失效，也不能再登录
	if _, err := env.DB.NewRaw("UPDATE members SET disabled_at = ? WHERE id = (SELECT owner_id FROM tenants WHERE slug = 'other')", time.Now().UTC()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if r := call(h, "GET", "/api/me", "", otherLogin.cookie); r.status != http.StatusUnauthorized { // 与 token 失效相同，都回到登录页
		t.Errorf("停用后的会话：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/api/auth/login", `{"email":"other@example.com","password":"password"}`, nil); r.status != http.StatusUnauthorized || r.code != "member.account_disabled" {
		t.Errorf("停用后登录：%d %s", r.status, r.body)
	}

	// 退出后会话失效
	if r := call(h, "POST", "/api/auth/logout", "", session); r.status != http.StatusNoContent {
		t.Fatalf("退出：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/api/me", "", session); r.status != http.StatusUnauthorized {
		t.Errorf("退出后：%d %s", r.status, r.body)
	}
}
