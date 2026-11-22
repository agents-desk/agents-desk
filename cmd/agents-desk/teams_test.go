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
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestTeams 在完整的路由上检查团队收件箱：新建和重名、加入成员、按角色的权限、我的团队收件箱、工作区之间隔离，
// 以及成员被移出工作区后团队收件箱跟着调整。
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
	roles := map[string]string{"admin": roleID(t, h, "alpha", "管理员", owner), "agent": roleID(t, h, "alpha", "坐席", owner)}
	betaAgent := roleID(t, h, "beta", "坐席", betaOwner)
	members := map[string]string{}
	for _, m := range []struct{ email, role string }{{"manager@alpha.com", "admin"}, {"agent@alpha.com", "agent"}} {
		r := call(h, "POST", "/t/alpha/api/members", `{"email":"`+m.email+`","name":"`+m.role+`","password":"password","role_id":"`+roles[m.role]+`"}`, owner)
		members[m.role] = idOf(r.body)
	}
	// agent 也是 beta 的成员，并在 beta 的团队中
	call(h, "POST", "/t/beta/api/members", `{"email":"agent@alpha.com","role_id":"`+betaAgent+`"}`, betaOwner)
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
	betaOnly := idOf(call(h, "POST", "/t/beta/api/members", `{"email":"only@beta.com","name":"only","password":"password","role_id":"`+betaAgent+`"}`, betaOwner).body)
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

// TestTeamAssignmentAndInUse 检查团队收件箱的分配方式，以及还在被使用时不能删除：还有没关闭的会话，
// 或者是渠道的默认团队收件箱。
func TestTeamAssignmentAndInUse(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
		crypt: env.Crypt, channels: channel.DefaultRegistry(),
	}
	h := a.routes(nil)
	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := call(h, "POST", "/api/auth/login", `{"email":"owner@alpha.com","password":"password"}`, nil)
	owner := r.cookie
	type team struct{ ID, Assignment string }
	decode := func(body string) team {
		var v team
		_ = json.Unmarshal([]byte(body), &v)
		return v
	}

	// 分配方式：新建时默认手动，可以改成轮流，只接受两种取值
	r = call(h, "POST", "/t/alpha/api/teams", `{"name":"一线"}`, owner)
	created := decode(r.body)
	if r.status != http.StatusCreated || created.Assignment != "manual" {
		t.Fatalf("新建：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/teams/"+created.ID, `{"name":"一线","assignment":"round_robin"}`, owner); decode(r.body).Assignment != "round_robin" {
		t.Fatalf("改成轮流：%d %s", r.status, r.body)
	}
	var changed string
	if err := env.DB.NewRaw("SELECT properties->>'changed' FROM activity_log WHERE event = 'team.updated' AND target_id = ?", created.ID).Scan(ctx, &changed); err != nil || changed != `["assignment"]` {
		t.Errorf("操作日志：%q %v", changed, err)
	}
	if r := call(h, "POST", "/t/alpha/api/teams", `{"name":"二线","assignment":"random"}`, owner); r.status != http.StatusUnprocessableEntity || fieldOf(r.body) != "body.assignment" {
		t.Errorf("不认识的分配方式：%d %s", r.status, r.body)
	}

	// 渠道以它为默认团队收件箱，客户发来消息，会话进入它
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试", TeamID: &created.ID})
	if err != nil {
		t.Fatal(err)
	}
	received, err := conversation.NewReceive(env.DB, a.channels, a.downstream()).Handle(ctx, system, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "rahul", Text: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 还在被使用时不能删除，提示中有会话数和渠道数
	if r := call(h, "DELETE", "/t/alpha/api/teams/"+created.ID, "", owner); r.code != "team.in_use" || !strings.Contains(r.body, "1 个会话、1 个渠道") {
		t.Fatalf("删除使用中的团队收件箱：%d %s", r.status, r.body)
	}
	// 关闭会话、解绑渠道后，因为仍有历史会话和关闭记录引用，依然不能删除（改为停用，保留历史会话与报表名称）
	if r := call(h, "POST", "/t/alpha/api/conversations/"+received.ConversationID+"/close", "", owner); r.status != http.StatusOK {
		t.Fatalf("关闭会话：%d %s", r.status, r.body)
	}
	if r := call(h, "DELETE", "/t/alpha/api/teams/"+created.ID, "", owner); r.code != "team.in_use" || !strings.Contains(r.body, "1 个会话、1 个渠道") {
		t.Fatalf("关闭后仍有历史会话和渠道：%d %s", r.status, r.body)
	}
	// 停用：允许有会话和渠道引用的团队收件箱直接停用，重复停用幂等
	if r := call(h, "POST", "/t/alpha/api/teams/"+created.ID+"/disable", "", owner); r.status != http.StatusOK || !strings.Contains(r.body, `"status":"disabled"`) {
		t.Fatalf("停用团队收件箱：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/teams/"+created.ID+"/disable", "", owner); r.status != http.StatusOK {
		t.Fatalf("重复停用：%d %s", r.status, r.body)
	}
	// 停用后：状态筛选可见，但收件箱侧栏隐藏
	if r := call(h, "GET", "/t/alpha/api/teams?status=disabled", "", owner); !strings.Contains(r.body, created.ID) {
		t.Fatalf("筛选已停用：%s", r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/teams?status=active", "", owner); strings.Contains(r.body, created.ID) {
		t.Fatalf("筛选启用中不应包含已停用：%s", r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/inbox", "", owner); strings.Contains(r.body, created.ID) {
		t.Fatalf("收件箱侧栏应隐藏已停用团队：%s", r.body)
	}
	// 停用后：渠道仍指向该团队时，新会话不再进入它（落入未指派）
	received2, err := conversation.NewReceive(env.DB, a.channels, a.downstream()).Handle(ctx, system, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "priya", Text: "need help",
	})
	if err != nil {
		t.Fatal(err)
	}
	var nextTeam *string
	if err := env.DB.NewRaw("SELECT team_id FROM conversations WHERE id = ?", received2.ConversationID).Scan(ctx, &nextTeam); err != nil || nextTeam != nil {
		t.Errorf("停用团队不应接收新会话：%v %v", nextTeam, err)
	}
	// 停用后：不能手动指派到已停用的团队收件箱
	if r := call(h, "PUT", "/t/alpha/api/conversations/"+received2.ConversationID+"/assignment", `{"team_id":"`+created.ID+`"}`, owner); r.code != "team.disabled" || fieldOf(r.body) != "body.team_id" {
		t.Errorf("指派到已停用团队：%d %s", r.status, r.body)
	}
	// 停用后：新建聊天插件不能选择已停用的团队收件箱
	if r := call(h, "POST", "/t/alpha/api/widgets", `{"name":"官网","team_id":"`+created.ID+`"}`, owner); r.code != "team.disabled" || fieldOf(r.body) != "body.team_id" {
		t.Errorf("新建聊天插件选择已停用团队：%d %s", r.status, r.body)
	}
	// 历史会话仍保留原团队收件箱 ID，供历史查看和报表使用
	var histTeam *string
	if err := env.DB.NewRaw("SELECT team_id FROM conversations WHERE id = ?", received.ConversationID).Scan(ctx, &histTeam); err != nil || histTeam == nil || *histTeam != created.ID {
		t.Errorf("历史会话应保留团队：%v %v", histTeam, err)
	}
	// 解绑渠道后仍因历史会话不能删除；空的团队收件箱可以删除
	if _, err := env.DB.NewRaw("UPDATE channels SET team_id = NULL WHERE id = ?", ch.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if r := call(h, "DELETE", "/t/alpha/api/teams/"+created.ID, "", owner); r.code != "team.in_use" || !strings.Contains(r.body, "1 个会话、0 个渠道") {
		t.Fatalf("有历史会话不能删除：%d %s", r.status, r.body)
	}
	// 重新启用后恢复正常，空团队可直接删除
	if r := call(h, "POST", "/t/alpha/api/teams/"+created.ID+"/enable", "", owner); r.status != http.StatusOK || !strings.Contains(r.body, `"status":"active"`) {
		t.Fatalf("重新启用：%d %s", r.status, r.body)
	}
	emptyTeam := decode(call(h, "POST", "/t/alpha/api/teams", `{"name":"空组"}`, owner).body)
	if r := call(h, "DELETE", "/t/alpha/api/teams/"+emptyTeam.ID, "", owner); r.status != http.StatusNoContent {
		t.Fatalf("删除空团队：%d %s", r.status, r.body)
	}

	// 工作区常规设置：默认重开窗口 24 小时，可以修改和校验范围
	if r := call(h, "GET", "/t/alpha/api/tenant", "", owner); r.status != http.StatusOK || !strings.Contains(r.body, `"reopen_window_hours":24`) {
		t.Fatalf("默认重开窗口：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/tenant", `{"name":"alpha","reopen_window_hours":48}`, owner); r.status != http.StatusOK || !strings.Contains(r.body, `"reopen_window_hours":48`) {
		t.Fatalf("修改重开窗口：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/tenant", `{"name":"alpha","reopen_window_hours":-1}`, owner); r.status != http.StatusUnprocessableEntity {
		t.Errorf("负数重开窗口：%d %s", r.status, r.body)
	}
}
