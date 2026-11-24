package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestRoles 在完整的路由上检查工作区的角色：初始角色、新建和重名、只能勾选工作区的操作权限、修改后立即生效、
// 还有人使用时不能删除、按角色的权限，以及主管理员和别的工作区的角色访问不到。
func TestRoles(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, localDisk: env.Disk,
	}
	h := a.routes(nil)
	create := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
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
	type role struct {
		ID, Name          string
		ConversationScope string   `json:"conversation_scope"`
		Permissions       []string `json:"permissions"`
		MemberCount       int      `json:"member_count"`
	}
	decode := func(body string) role {
		var r role
		_ = json.Unmarshal([]byte(body), &r)
		return r
	}
	permissions := func(cookie *http.Cookie) string {
		t.Helper()
		return call(h, "GET", "/t/alpha/api/tenant", "", cookie).body
	}
	owner, betaOwner := login("owner@alpha.com"), login("owner@beta.com")

	// 初始角色：管理员看到全部会话、可以做全部操作；坐席看到所在团队的会话。主管理员不在列表中
	r := call(h, "GET", "/t/alpha/api/roles", "", owner)
	var initial struct{ Items []role }
	_ = json.Unmarshal([]byte(r.body), &initial)
	if len(initial.Items) != 2 || initial.Items[0].Name != "坐席" || initial.Items[1].Name != "管理员" {
		t.Fatalf("初始角色：%s", r.body)
	}
	if got := initial.Items[0]; got.ConversationScope != "team" || len(got.Permissions) != 0 {
		t.Errorf("坐席：%+v", got)
	}
	if got := initial.Items[1]; got.ConversationScope != "all" || len(got.Permissions) != 9 {
		t.Errorf("管理员：%+v", got)
	}
	// 角色中只保存可见范围和勾选的操作，基本权限在加载成员的权限时加上
	var stored []string
	if err := env.DB.NewRaw("SELECT permission FROM authz_role_permissions WHERE role_id = ?", initial.Items[0].ID).Scan(ctx, &stored); err != nil || len(stored) != 1 || stored[0] != "conversations:read-team" {
		t.Errorf("坐席保存的权限：%v %v", stored, err)
	}

	// 新建：同一个工作区中不重名；只能勾选工作区的操作权限
	r = call(h, "POST", "/t/alpha/api/roles", `{"name":"质检","description":"抽查会话","conversation_scope":"assigned","permissions":[]}`, owner)
	if r.status != http.StatusCreated || decode(r.body).ConversationScope != "assigned" {
		t.Fatalf("新建角色：%d %s", r.status, r.body)
	}
	qa := decode(r.body).ID
	if r := call(h, "POST", "/t/alpha/api/roles", `{"name":"质检","description":"","conversation_scope":"all","permissions":[]}`, owner); r.code != "role.name_taken" || fieldOf(r.body) != "body.name" {
		t.Errorf("重名：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/roles", `{"name":"越权","description":"","conversation_scope":"all","permissions":["tenants:manage"]}`, owner); r.status != http.StatusUnprocessableEntity || fieldOf(r.body) != "body.permissions[0]" {
		t.Errorf("平台的权限：%d %s", r.status, r.body)
	}

	// 角色为质检的成员：能看成员和角色列表（供成员列表筛选器使用）、能看会话，只能看指派给自己的；不能管理角色
	call(h, "POST", "/t/alpha/api/members", `{"email":"qa@alpha.com","name":"质检员","password":"password","role_id":"`+qa+`"}`, owner)
	inspector := login("qa@alpha.com")
	if got := permissions(inspector); !strings.Contains(got, `"conversations:read"`) || strings.Contains(got, "conversations:read-") || strings.Contains(got, "members:manage") {
		t.Fatalf("质检员的权限：%s", got)
	}
	if r := call(h, "GET", "/t/alpha/api/members", "", inspector); r.status != http.StatusOK {
		t.Errorf("质检员看成员：%d", r.status)
	}
	if r := call(h, "GET", "/t/alpha/api/roles", "", inspector); r.status != http.StatusOK {
		t.Errorf("质检员看角色列表：%d", r.status)
	}
	if r := call(h, "POST", "/t/alpha/api/roles", `{"name":"x","description":"","conversation_scope":"assigned","permissions":[]}`, inspector); r.status != http.StatusForbidden {
		t.Errorf("质检员建角色：%d", r.status)
	}
	if r := call(h, "GET", "/t/alpha/api/roles/"+qa, "", owner); decode(r.body).MemberCount != 1 {
		t.Errorf("人数：%s", r.body)
	}

	// 修改角色：下一个请求起按新的权限检查；改成已有的名称时重名
	r = call(h, "PUT", "/t/alpha/api/roles/"+qa, `{"name":"质检","description":"抽查会话","conversation_scope":"team","permissions":["members:manage"]}`, owner)
	if r.status != http.StatusOK || decode(r.body).ConversationScope != "team" || len(decode(r.body).Permissions) != 1 {
		t.Fatalf("修改角色：%d %s", r.status, r.body)
	}
	// 防自我提权：只拥有 team 范围 + members:manage 的质检员，不能创建或修改出超过自身权限的角色，也不能给别人分配更高权限的角色
	if r := call(h, "POST", "/t/alpha/api/roles", `{"name":"超权范围","description":"","conversation_scope":"all","permissions":[]}`, inspector); r.status != http.StatusForbidden || r.code != "authz.exceeds_permissions" {
		t.Errorf("超权可见范围：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/roles", `{"name":"超权操作","description":"","conversation_scope":"team","permissions":["channels:manage"]}`, inspector); r.status != http.StatusForbidden || r.code != "authz.exceeds_permissions" {
		t.Errorf("超权操作权限：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/roles/"+qa, `{"name":"质检","description":"","conversation_scope":"all","permissions":["members:manage"]}`, inspector); r.status != http.StatusForbidden || r.code != "authz.exceeds_permissions" {
		t.Errorf("修改角色提权：%d %s", r.status, r.body)
	}
	adminRole := roleID(t, h, "alpha", "管理员", owner)
	if r := call(h, "POST", "/t/alpha/api/members", `{"email":"escalate@alpha.com","name":"提权","password":"password","role_id":"`+adminRole+`"}`, inspector); r.status != http.StatusForbidden || r.code != "authz.exceeds_permissions" {
		t.Errorf("分配超权角色：%d %s", r.status, r.body)
	}
	if got := permissions(inspector); !strings.Contains(got, `"conversations:read-team"`) {
		t.Errorf("修改后的权限：%s", got)
	}
	if r := call(h, "PUT", "/t/alpha/api/roles/"+qa, `{"name":"坐席","description":"","conversation_scope":"team","permissions":[]}`, owner); r.code != "role.name_taken" {
		t.Errorf("改成已有的名称：%d %s", r.status, r.body)
	}

	// 删除：还有人使用时不能删除，没人使用后可以删除
	if r := call(h, "DELETE", "/t/alpha/api/roles/"+qa, "", owner); r.code != "role.in_use" || !strings.Contains(r.body, "1 位成员") {
		t.Fatalf("删除使用中的角色：%d %s", r.status, r.body)
	}
	var found struct{ Items []struct{ ID string } }
	_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/members?name="+url.QueryEscape("质检员"), "", owner).body), &found)
	if len(found.Items) != 1 {
		t.Fatalf("质检员：%+v", found)
	}
	qaMember := found.Items[0].ID
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	if r := call(h, "PUT", "/t/alpha/api/members/"+qaMember, `{"role_id":"`+agentRole+`"}`, owner); r.status != http.StatusOK {
		t.Fatalf("改成坐席：%d %s", r.status, r.body)
	}
	if r := call(h, "DELETE", "/t/alpha/api/roles/"+qa, "", owner); r.status != http.StatusNoContent {
		t.Fatalf("删除角色：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/roles/"+qa, "", owner); r.code != "role.not_found" {
		t.Errorf("删除后：%d %s", r.status, r.body)
	}

	// 坐席可以读取角色列表（供成员筛选），但不能新建、修改、删除角色
	if r := call(h, "GET", "/t/alpha/api/roles", "", inspector); r.status != http.StatusOK {
		t.Errorf("坐席 GET /t/alpha/api/roles：%d %s", r.status, r.body)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/t/alpha/api/roles", `{"name":"x","description":"","conversation_scope":"all","permissions":[]}`},
		{"PUT", "/t/alpha/api/roles/" + agentRole, `{"name":"x","description":"","conversation_scope":"all","permissions":[]}`},
		{"DELETE", "/t/alpha/api/roles/" + agentRole, ""},
	} {
		if r := call(h, c.method, c.path, c.body, inspector); r.status != http.StatusForbidden {
			t.Errorf("坐席 %s %s：%d %s", c.method, c.path, r.status, r.body)
		}
	}

	// 主管理员和别的工作区的角色：读、改、删都当作不存在
	ownerRole, err := env.Roles.RoleByName(ctx, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ownerRole.ID, roleID(t, h, "beta", "坐席", betaOwner)} {
		for _, c := range []struct{ method, body string }{
			{"GET", ""},
			{"PUT", `{"name":"x","description":"","conversation_scope":"all","permissions":[]}`},
			{"DELETE", ""},
		} {
			if r := call(h, c.method, "/t/alpha/api/roles/"+id, c.body, owner); r.status != http.StatusNotFound || r.code != "role.not_found" {
				t.Errorf("%s 别处的角色 %s：%d %s", c.method, id, r.status, r.body)
			}
		}
	}
}
