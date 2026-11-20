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

// TestTeams 在完整的路由上检查团队：新建和重名、加入成员、按角色的权限、我的团队、工作区之间隔离，
// 以及成员被移出工作区后团队跟着调整。
func TestTeams(t *testing.T) {
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
	idOf := func(body string) string {
		var v struct{ ID string }
		_ = json.Unmarshal([]byte(body), &v)
		return v.ID
	}
	owner, betaOwner := login("owner@alpha.com"), login("owner@beta.com")
	members := map[string]string{}
	for _, m := range []struct{ email, role string }{{"manager@alpha.com", "admin"}, {"agent@alpha.com", "agent"}} {
		r := call(h, "POST", "/t/alpha/api/members", `{"email":"`+m.email+`","name":"`+m.role+`","password":"password","role":"`+m.role+`"}`, owner)
		members[m.role] = idOf(r.body)
	}
	// agent 也是 beta 的成员，并在 beta 的团队中
	call(h, "POST", "/t/beta/api/members", `{"email":"agent@alpha.com","role":"agent"}`, betaOwner)
	betaTeam := idOf(call(h, "POST", "/t/beta/api/teams", `{"name":"一线"}`, betaOwner).body)
	if r := call(h, "PUT", "/t/beta/api/teams/"+betaTeam+"/members/"+members["agent"], "", betaOwner); r.status != http.StatusOK {
		t.Fatalf("beta 的团队加入 agent：%d %s", r.status, r.body)
	}

	// 新建：同一个工作区中不重名，不同工作区可以重名（beta 已经有“一线”）
	r := call(h, "POST", "/t/alpha/api/teams", `{"name":"一线","description":"第一次咨询"}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建团队：%d %s", r.status, r.body)
	}
	team := idOf(r.body)
	if r := call(h, "POST", "/t/alpha/api/teams", `{"name":"一线"}`, owner); r.code != "team.name_taken" || fieldOf(r.body) != "body.name" {
		t.Fatalf("重名：%d %s", r.status, r.body)
	}

	// 加入成员：任何角色都可以加入，重复加入保持原样
	put := func(memberID string, cookie *http.Cookie) response {
		return call(h, "PUT", "/t/alpha/api/teams/"+team+"/members/"+memberID, "", cookie)
	}
	for _, key := range []string{"agent", "admin", "agent"} {
		if r := put(members[key], owner); r.status != http.StatusOK || !strings.Contains(r.body, `"member_id":"`+members[key]+`"`) {
			t.Fatalf("加入 %s：%d %s", key, r.status, r.body)
		}
	}
	if r := call(h, "GET", "/t/alpha/api/teams/"+team, "", owner); !strings.Contains(r.body, `"member_count":2`) {
		t.Fatalf("团队：%s", r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/teams/"+team+"/members", "", owner); !strings.HasPrefix(r.body, `[{"member_id":"`+members["admin"]) {
		t.Fatalf("按名字排列：%s", r.body)
	}
	var added int
	if err := env.DB.NewRaw("SELECT count(*) FROM activity_log WHERE event = 'team.member_added' AND target_id = ?", team).Scan(ctx, &added); err != nil || added != 2 {
		t.Fatalf("加入的操作日志：%d %v", added, err)
	}

	// 坐席：能看团队和自己的团队，不能管理
	agent := login("agent@alpha.com")
	if r := call(h, "GET", "/t/alpha/api/teams?mine=true", "", agent); r.status != http.StatusOK || !strings.Contains(r.body, team) {
		t.Fatalf("我的团队：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/teams?mine=true", "", owner); strings.Contains(r.body, team) {
		t.Fatalf("主管理员不在这个团队中：%s", r.body)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/t/alpha/api/teams", `{"name":"二线"}`},
		{"PUT", "/t/alpha/api/teams/" + team, `{"name":"改名"}`},
		{"DELETE", "/t/alpha/api/teams/" + team, ""},
		{"PUT", "/t/alpha/api/teams/" + team + "/members/" + members["agent"], ""},
		{"DELETE", "/t/alpha/api/teams/" + team + "/members/" + members["agent"], ""},
	} {
		if r := call(h, c.method, c.path, c.body, agent); r.status != http.StatusForbidden {
			t.Errorf("坐席 %s %s：%d %s", c.method, c.path, r.status, r.body)
		}
	}

	// 工作区之间隔离：别的工作区的人不能加入，别的工作区的团队看不到
	betaOnly := idOf(call(h, "POST", "/t/beta/api/members", `{"email":"only@beta.com","name":"only","password":"password","role":"agent"}`, betaOwner).body)
	if r := put(betaOnly, owner); r.status != http.StatusNotFound || r.code != "member.not_found" {
		t.Errorf("加入别的工作区的人：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/teams/"+betaTeam, "", owner); r.code != "team.not_found" {
		t.Errorf("别的工作区的团队：%d %s", r.status, r.body)
	}

	// 移出工作区：也就移出了这个工作区的团队，别的工作区的团队不受影响
	if r := call(h, "POST", "/t/alpha/api/members/"+members["agent"]+"/remove", "", owner); r.status != http.StatusNoContent {
		t.Fatalf("移出工作区：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/teams/"+team+"/members", "", owner); strings.Contains(r.body, members["agent"]) {
		t.Errorf("移出工作区后仍在团队中：%s", r.body)
	}
	if r := call(h, "GET", "/t/beta/api/teams/"+betaTeam+"/members", "", betaOwner); !strings.Contains(r.body, members["agent"]) {
		t.Errorf("别的工作区的团队：%s", r.body)
	}
	// 移出团队、删除团队
	if r := call(h, "DELETE", "/t/alpha/api/teams/"+team+"/members/"+members["agent"], "", owner); r.code != "team.not_in_team" {
		t.Errorf("移出不在团队中的人：%d %s", r.status, r.body)
	}
	if r := call(h, "DELETE", "/t/alpha/api/teams/"+team+"/members/"+members["admin"], "", owner); r.status != http.StatusNoContent {
		t.Errorf("移出团队：%d %s", r.status, r.body)
	}
	if r := call(h, "DELETE", "/t/alpha/api/teams/"+team, "", owner); r.status != http.StatusNoContent {
		t.Fatalf("删除团队：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/teams/"+team, "", owner); r.status != http.StatusNotFound {
		t.Errorf("删除后：%d", r.status)
	}

	// 成员列表中自己那一行 self 为 true，前端据此不显示“修改”“移出”
	if r := call(h, "GET", "/t/alpha/api/members?role=owner", "", owner); !strings.Contains(r.body, `"self":true`) {
		t.Errorf("自己那一行：%s", r.body)
	}
}
