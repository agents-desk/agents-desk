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
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestMemberManagement 在完整的路由上检查工作区的成员管理：添加（新账号、别的工作区已有的账号）、修改角色、移出，
// 主管理员和自己受保护，按角色的权限，工作区之间隔离，以及修改工作区名称。
func TestMemberManagement(t *testing.T) {
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
	create := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{})
	for _, slug := range []string{"alpha", "beta"} {
		if _, err := create.Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: slug + " 主管理员", OwnerPassword: "password",
		}); err != nil {
			t.Fatal(err)
		}
	}
	login := func(email string) *http.Cookie {
		t.Helper()
		r := call(h, "POST", "/api/auth/login", `{"email":"`+email+`","password":"password"}`, nil)
		if r.status != http.StatusNoContent {
			t.Fatalf("登录 %s：%d %s", email, r.status, r.body)
		}
		return r.cookie
	}
	type item struct{ ID, Name, Email, Role string }
	decode := func(body string) item {
		var m item
		_ = json.Unmarshal([]byte(body), &m)
		return m
	}
	list := func(cookie *http.Cookie, query string) []item {
		t.Helper()
		r := call(h, "GET", "/t/alpha/api/members"+query, "", cookie)
		var p struct{ Items []item }
		if err := json.Unmarshal([]byte(r.body), &p); err != nil || r.status != http.StatusOK {
			t.Fatalf("成员列表：%d %s", r.status, r.body)
		}
		return p.Items
	}

	owner := login("owner@alpha.com")
	// 主管理员的权限是明确的列表，不再是 *
	if r := call(h, "GET", "/t/alpha/api/tenant", "", owner); !strings.Contains(r.body, `"members:manage"`) || strings.Contains(r.body, `"*"`) {
		t.Fatalf("主管理员的权限：%s", r.body)
	}

	// 添加新账号：一位管理员、两位坐席
	ids := map[string]string{}
	for _, m := range []struct{ key, role string }{{"manager", "admin"}, {"other", "agent"}, {"agent", "agent"}} {
		r := call(h, "POST", "/t/alpha/api/members", `{"email":"`+m.key+`@alpha.com","name":"`+m.key+`","password":"password","role":"`+m.role+`"}`, owner)
		if r.status != http.StatusCreated || decode(r.body).Role != m.role {
			t.Fatalf("添加 %s：%d %s", m.key, r.status, r.body)
		}
		ids[m.key] = decode(r.body).ID
	}
	// 别的工作区已有的账号：直接加入，不改名字和密码；他可以在两个工作区之间切换
	r := call(h, "POST", "/t/alpha/api/members", `{"email":"owner@beta.com","name":"别的名字","role":"agent"}`, owner)
	if r.status != http.StatusCreated || decode(r.body).Name != "beta 主管理员" {
		t.Fatalf("添加已有账号：%d %s", r.status, r.body)
	}
	betaOwner := login("owner@beta.com")
	if r := call(h, "GET", "/api/tenants", "", betaOwner); !strings.Contains(r.body, `"alpha"`) || !strings.Contains(r.body, `"beta"`) {
		t.Fatalf("他所属的工作区：%s", r.body)
	}
	// 列表按角色从高到低排列，可以按角色和名字筛选
	if got := list(owner, ""); len(got) != 5 || got[0].Role != "owner" || got[1].Role != "admin" || got[2].Role != "agent" {
		t.Fatalf("成员列表：%+v", got)
	}
	if got := list(owner, "?role=agent"); len(got) != 3 {
		t.Fatalf("按角色筛选：%+v", got)
	}
	if got := list(owner, "?name=beta"); len(got) != 1 || got[0].Email != "owner@beta.com" {
		t.Fatalf("按名字筛选：%+v", got)
	}

	// 添加时的错误都指向字段
	for body, want := range map[string]string{
		`{"email":"agent@alpha.com","role":"agent"}`:                                 "body.email",    // 已经是成员
		`{"email":"new@alpha.com","name":"新人","role":"agent"}`:                       "body.password", // 新账号缺少密码
		`{"email":"new@alpha.com","password":"password","role":"agent"}`:             "body.name",     // 新账号缺少名字
		`{"email":"new@alpha.com","name":"新人","password":"password","role":"owner"}`: "body.role",     // 不能设成主管理员
	} {
		if r := call(h, "POST", "/t/alpha/api/members", body, owner); r.status < 400 || r.status >= 500 || fieldOf(r.body) != want {
			t.Errorf("%s：%d %s", body, r.status, r.body)
		}
	}

	// 坐席能看成员，不能管理成员和设置
	agent := login("agent@alpha.com")
	if got := list(agent, ""); len(got) != 5 {
		t.Fatalf("坐席看成员：%d", len(got))
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/t/alpha/api/members", `{"email":"x@alpha.com","name":"x","password":"password","role":"agent"}`},
		{"PUT", "/t/alpha/api/members/" + ids["other"], `{"role":"agent"}`},
		{"POST", "/t/alpha/api/members/" + ids["other"] + "/remove", ""},
		{"PUT", "/t/alpha/api/tenant", `{"name":"改名"}`},
	} {
		if r := call(h, c.method, c.path, c.body, agent); r.status != http.StatusForbidden {
			t.Errorf("坐席 %s %s：%d %s", c.method, c.path, r.status, r.body)
		}
	}

	// 管理员：可以改别人的角色、移出别人，不能动主管理员和自己
	manager := login("manager@alpha.com")
	var ownerID string
	for _, m := range list(manager, "?role=owner") {
		ownerID = m.ID
	}
	if r := call(h, "PUT", "/t/alpha/api/members/"+ids["other"], `{"role":"admin"}`, manager); r.status != http.StatusOK || decode(r.body).Role != "admin" {
		t.Fatalf("改角色：%d %s", r.status, r.body)
	}
	for _, c := range []struct{ method, path, body, code string }{
		{"PUT", "/t/alpha/api/members/" + ownerID, `{"role":"agent"}`, "member.owner_protected"},
		{"POST", "/t/alpha/api/members/" + ownerID + "/remove", "", "member.owner_protected"},
		{"PUT", "/t/alpha/api/members/" + ids["manager"], `{"role":"agent"}`, "member.self"},
		{"POST", "/t/alpha/api/members/" + ids["manager"] + "/remove", "", "member.self"},
	} {
		if r := call(h, c.method, c.path, c.body, manager); r.code != c.code {
			t.Errorf("%s %s：%d %s", c.method, c.path, r.status, r.body)
		}
	}
	// 移出：他再进这个工作区是 404，账号还在，别的工作区不受影响
	other := login("other@alpha.com")
	if r := call(h, "POST", "/t/alpha/api/members/"+ids["other"]+"/remove", "", manager); r.status != http.StatusNoContent {
		t.Fatalf("移出：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/tenant", "", other); r.status != http.StatusNotFound {
		t.Errorf("移出后进入工作区：%d", r.status)
	}
	if r := call(h, "GET", "/api/me", "", other); r.status != http.StatusOK {
		t.Errorf("移出后的账号：%d", r.status)
	}
	if r := call(h, "GET", "/t/alpha/api/members/"+ids["other"], "", manager); r.status != http.StatusNotFound {
		t.Errorf("移出后读取：%d %s", r.status, r.body)
	}

	// 工作区之间隔离：alpha 的主管理员进不了 beta；beta 的成员在 alpha 中查不到
	if r := call(h, "GET", "/t/beta/api/members", "", owner); r.status != http.StatusNotFound {
		t.Errorf("进入别的工作区：%d", r.status)
	}
	var betaOnly string
	if r := call(h, "POST", "/t/beta/api/members", `{"email":"only@beta.com","name":"only","password":"password","role":"agent"}`, betaOwner); r.status == http.StatusCreated {
		betaOnly = decode(r.body).ID
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/t/alpha/api/members/" + betaOnly, ""},
		{"PUT", "/t/alpha/api/members/" + betaOnly, `{"role":"admin"}`},
		{"POST", "/t/alpha/api/members/" + betaOnly + "/remove", ""},
	} {
		if r := call(h, c.method, c.path, c.body, owner); r.status != http.StatusNotFound {
			t.Errorf("别的工作区的成员 %s：%d %s", c.method, r.status, r.body)
		}
	}

	// 修改工作区名称
	if r := call(h, "PUT", "/t/alpha/api/tenant", `{"name":"阿尔法"}`, manager); r.status != http.StatusOK || !strings.Contains(r.body, `"name":"阿尔法"`) {
		t.Fatalf("改名：%d %s", r.status, r.body)
	}
}
