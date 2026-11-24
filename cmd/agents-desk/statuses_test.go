package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestMemberStatus 在完整的路由上检查在线状态：默认离开，自己切换在线和离开，每个工作区分开设置，成员列表和团队收件箱的
// 成员中显示状态，管理员能把别人设为离开，以及移出成员后他的状态被删除。
func TestMemberStatus(t *testing.T) {
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
	type setting struct {
		Status   string
		Reassign bool
	}
	status := func(slug string, cookie *http.Cookie) setting {
		t.Helper()
		var s setting
		_ = json.Unmarshal([]byte(call(h, "GET", "/t/"+slug+"/api/me/status", "", cookie).body), &s)
		return s
	}
	memberStatus := func(id string, cookie *http.Cookie) string {
		t.Helper()
		var m struct{ Status string }
		_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/members/"+id, "", cookie).body), &m)
		return m.Status
	}
	owner, betaOwner := login("owner@alpha.com"), login("owner@beta.com")

	// 坐席同时在 alpha 和 beta 中，在一个团队收件箱中
	r := call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"坐席","password":"password","role_id":"`+roleID(t, h, "alpha", "坐席", owner)+`"}`, owner)
	var created struct{ ID, Status string }
	_ = json.Unmarshal([]byte(r.body), &created)
	if created.Status != "away" {
		t.Fatalf("新成员默认离开：%s", r.body)
	}
	call(h, "POST", "/t/beta/api/members", `{"email":"agent@alpha.com","role_id":"`+roleID(t, h, "beta", "坐席", betaOwner)+`"}`, betaOwner)
	var team struct{ ID string }
	_ = json.Unmarshal([]byte(call(h, "POST", "/t/alpha/api/teams", `{"name":"一线"}`, owner).body), &team)
	call(h, "PUT", "/t/alpha/api/teams/"+team.ID+"/members/"+created.ID, "", owner)
	agent := login("agent@alpha.com")

	// 默认离开；切换到在线只影响这个工作区
	if got := status("alpha", agent); got.Status != "away" || got.Reassign {
		t.Fatalf("默认：%+v", got)
	}
	if r := call(h, "PUT", "/t/alpha/api/me/status", `{"status":"online","reassign":false}`, agent); r.status != http.StatusOK {
		t.Fatalf("切换到在线：%d %s", r.status, r.body)
	}
	if got := status("beta", agent); got.Status != "away" {
		t.Errorf("别的工作区：%+v", got)
	}
	if got := memberStatus(created.ID, owner); got != "online" {
		t.Errorf("成员中的状态：%s", got)
	}
	var teamMembers []struct{ Status string }
	_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/teams/"+team.ID+"/members", "", owner).body), &teamMembers)
	if len(teamMembers) != 1 || teamMembers[0].Status != "online" {
		t.Errorf("团队收件箱的成员：%+v", teamMembers)
	}
	if r := call(h, "PUT", "/t/alpha/api/me/status", `{"status":"busy","reassign":false}`, agent); r.status != http.StatusUnprocessableEntity {
		t.Errorf("不认识的状态：%d %s", r.status, r.body)
	}

	// 离开并勾选重新分配；切回在线后保留这个选择
	call(h, "PUT", "/t/alpha/api/me/status", `{"status":"away","reassign":true}`, agent)
	if got := status("alpha", agent); got.Status != "away" || !got.Reassign {
		t.Fatalf("离开并重新分配：%+v", got)
	}
	call(h, "PUT", "/t/alpha/api/me/status", `{"status":"online","reassign":true}`, agent)

	// 管理员把在线的人设为离开，保留他选的重新分配；坐席不能设别人
	if r := call(h, "POST", "/t/alpha/api/members/"+created.ID+"/away", "", owner); r.status != http.StatusNoContent {
		t.Fatalf("设为离开：%d %s", r.status, r.body)
	}
	if got := status("alpha", agent); got.Status != "away" || !got.Reassign {
		t.Errorf("被设为离开：%+v", got)
	}
	var ownerID string
	var members struct {
		Items []struct {
			ID    string
			Owner bool
		}
	}
	_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/members", "", owner).body), &members)
	for _, m := range members.Items {
		if m.Owner {
			ownerID = m.ID
		}
	}
	if r := call(h, "POST", "/t/alpha/api/members/"+ownerID+"/away", "", agent); r.status != http.StatusForbidden {
		t.Errorf("坐席设别人：%d %s", r.status, r.body)
	}
	var betaOnly struct{ ID string }
	_ = json.Unmarshal([]byte(call(h, "POST", "/t/beta/api/members", `{"email":"only@beta.com","name":"only","password":"password","role_id":"`+roleID(t, h, "beta", "坐席", betaOwner)+`"}`, betaOwner).body), &betaOnly)
	if r := call(h, "POST", "/t/alpha/api/members/"+betaOnly.ID+"/away", "", owner); r.code != "member.not_found" {
		t.Errorf("别的工作区的成员：%d %s", r.status, r.body)
	}

	// 移出成员：他在这个工作区的状态删除，别的工作区的保留
	call(h, "PUT", "/t/beta/api/me/status", `{"status":"online","reassign":false}`, agent)
	if r := call(h, "POST", "/t/alpha/api/members/"+created.ID+"/remove", "", owner); r.status != http.StatusNoContent {
		t.Fatalf("移出：%d %s", r.status, r.body)
	}
	var n int
	if err := env.DB.NewRaw("SELECT count(*) FROM member_statuses WHERE member_id = ?", created.ID).Scan(ctx, &n); err != nil || n != 1 {
		t.Errorf("移出后的状态：%d %v", n, err)
	}
	if got := status("beta", agent); got.Status != "online" {
		t.Errorf("别的工作区的状态：%+v", got)
	}
}
