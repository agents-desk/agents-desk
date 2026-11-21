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

// TestConversationActions 在完整的路由上检查会话的操作：指派的权限（领取、转出自己的、改派别人的）、转到别的团队收件箱时
// 清空负责人、关闭、重新打开、稍后处理和到期、优先级、批量操作，每个操作在时间线中留下系统事件，没有变化时什么也不写。
func TestConversationActions(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
		channels: channel.DefaultRegistry(),
	}
	h := a.routes(nil)
	tenants := map[string]string{}
	for _, slug := range []string{"alpha", "beta"} {
		tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: slug + " 主管理员", OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		tenants[slug] = tn.ID
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
	// 两位坐席在 A 线；坐席没有“把会话改派给别人”的权限，主管理员有
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	members := map[string]string{}
	for _, key := range []string{"agent1", "agent2"} {
		members[key] = idOf(call(h, "POST", "/t/alpha/api/members", `{"email":"`+key+`@alpha.com","name":"`+key+`","password":"password","role_id":"`+agentRole+`"}`, owner).body)
	}
	agent1, agent2 := login("agent1@alpha.com"), login("agent2@alpha.com")
	teamA := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"A线"}`, owner).body)
	teamB := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"B线"}`, owner).body)
	for _, m := range members {
		call(h, "PUT", "/t/alpha/api/teams/"+teamA+"/members/"+m, "", owner)
	}
	betaTeam := idOf(call(h, "POST", "/t/beta/api/teams", `{"name":"别处"}`, betaOwner).body)

	// 三个会话都进入 A 线
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tenants["alpha"]}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试", TeamID: &teamA})
	if err != nil {
		t.Fatal(err)
	}
	receive := conversation.NewReceive(env.DB, a.channels, a.downstream())
	start := func(from string) string {
		t.Helper()
		res, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: ch.ID, From: from, Text: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		return res.ConversationID
	}
	c1, c2, c3 := start("c1"), start("c2"), start("c3")

	type conv struct {
		Status       string
		TeamID       *string `json:"team_id"`
		Assignee     *struct{ ID string }
		Priority     string
		LastSeq      int64      `json:"last_seq"`
		ClosedAt     *time.Time `json:"closed_at"`
		SnoozedUntil *time.Time `json:"snoozed_until"`
	}
	do := func(method, id, action, body string, cookie *http.Cookie) (conv, response) {
		t.Helper()
		r := call(h, method, "/t/alpha/api/conversations/"+id+"/"+action, body, cookie)
		var c conv
		_ = json.Unmarshal([]byte(r.body), &c)
		return c, r
	}
	get := func(id string) conv {
		t.Helper()
		var c conv
		_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/conversations/"+id, "", owner).body), &c)
		return c
	}
	// lastEvent 返回会话时间线中最新一项：它的系统事件名称和发起者的类型
	lastEvent := func(id string) (string, string) {
		t.Helper()
		var items []struct {
			Content struct{ Event string }
			Actor   struct{ Type string }
		}
		_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/conversations/"+id+"/items", "", owner).body), &items)
		last := items[len(items)-1]
		return last.Content.Event, last.Actor.Type
	}
	assigneeOf := func(c conv) string {
		if c.Assignee == nil {
			return ""
		}
		return c.Assignee.ID
	}

	// 领取：没有负责人的会话，坐席指派给自己，团队收件箱不变
	c, r := do("PUT", c1, "assignment", `{"assignee_id":"`+members["agent1"]+`"}`, agent1)
	if r.status != http.StatusOK || assigneeOf(c) != members["agent1"] || c.TeamID == nil || *c.TeamID != teamA {
		t.Fatalf("领取：%d %s", r.status, r.body)
	}
	if ev, _ := lastEvent(c1); ev != "conversation.assigned" {
		t.Errorf("领取的系统事件：%s", ev)
	}
	// 别人负责的会话，没有改派的权限时不能抢过来：已经被别人领取了
	if _, r := do("PUT", c1, "assignment", `{"assignee_id":"`+members["agent2"]+`"}`, agent2); r.code != "conversation.already_assigned" {
		t.Errorf("抢别人的会话：%d %s", r.status, r.body)
	}
	// 自己负责的会话可以转给同事
	if c, r := do("PUT", c1, "assignment", `{"assignee_id":"`+members["agent2"]+`"}`, agent1); r.status != http.StatusOK || assigneeOf(c) != members["agent2"] {
		t.Errorf("转给同事：%d %s", r.status, r.body)
	}
	// 没有负责人的会话，坐席不能转到别的团队收件箱，也不能指派给别人
	for _, body := range []string{`{"team_id":"` + teamB + `"}`, `{"assignee_id":"` + members["agent2"] + `"}`} {
		if _, r := do("PUT", c2, "assignment", body, agent1); r.code != "conversation.forbidden" {
			t.Errorf("坐席改派 %s：%d %s", body, r.status, r.body)
		}
	}
	// 有改派的权限：指派给别人时团队收件箱不变；转到别的团队收件箱而没有指定负责人时清空负责人
	if c, _ := do("PUT", c2, "assignment", `{"assignee_id":"`+members["agent2"]+`"}`, owner); assigneeOf(c) != members["agent2"] || *c.TeamID != teamA {
		t.Errorf("主管理员指派：%+v", c)
	}
	if c, _ := do("PUT", c2, "assignment", `{"team_id":"`+teamB+`"}`, owner); assigneeOf(c) != "" || *c.TeamID != teamB {
		t.Errorf("转到 B 线：%+v", c)
	}
	if c, _ := do("PUT", c2, "assignment", `{"team_id":"`+teamA+`","assignee_id":"`+members["agent1"]+`"}`, owner); assigneeOf(c) != members["agent1"] || *c.TeamID != teamA {
		t.Errorf("转回 A 线并指定负责人：%+v", c)
	}
	// 没有变化时什么也不写；空字符串表示移出团队收件箱
	before := get(c2).LastSeq
	if c, _ := do("PUT", c2, "assignment", `{"assignee_id":"`+members["agent1"]+`"}`, owner); c.LastSeq != before {
		t.Errorf("没有变化时写了时间线：%d → %d", before, c.LastSeq)
	}
	if c, _ := do("PUT", c2, "assignment", `{"team_id":"","assignee_id":""}`, owner); c.TeamID != nil || c.Assignee != nil {
		t.Errorf("清空：%+v", c)
	}
	// 团队收件箱和负责人必须属于这个工作区
	if _, r := do("PUT", c2, "assignment", `{"team_id":"`+betaTeam+`"}`, owner); r.code != "team.not_found" || fieldOf(r.body) != "body.team_id" {
		t.Errorf("别的工作区的团队收件箱：%d %s", r.status, r.body)
	}
	betaMember := idOf(call(h, "POST", "/t/beta/api/members", `{"email":"x@beta.com","name":"x","password":"password","role_id":"`+roleID(t, h, "beta", "坐席", betaOwner)+`"}`, betaOwner).body)
	if _, r := do("PUT", c2, "assignment", `{"assignee_id":"`+betaMember+`"}`, owner); r.code != "member.not_found" || fieldOf(r.body) != "body.assignee_id" {
		t.Errorf("别的工作区的成员：%d %s", r.status, r.body)
	}
	if _, r := do("PUT", c2, "assignment", `{"team_id":"bad"}`, owner); r.status != http.StatusUnprocessableEntity {
		t.Errorf("不合法的 ID：%d %s", r.status, r.body)
	}

	// 关闭：记下关闭时间；再关闭一次什么也不写；重新打开
	c, r = do("POST", c3, "close", "", agent1)
	if r.status != http.StatusOK || c.Status != "closed" || c.ClosedAt == nil {
		t.Fatalf("关闭：%d %s", r.status, r.body)
	}
	if ev, actor := lastEvent(c3); ev != "conversation.closed" || actor != "member" {
		t.Errorf("关闭的系统事件：%s %s", ev, actor)
	}
	if again, _ := do("POST", c3, "close", "", agent1); again.LastSeq != c.LastSeq {
		t.Errorf("重复关闭写了时间线")
	}
	if _, r := do("POST", c3, "snooze", `{"until":"`+time.Now().Add(time.Hour).Format(time.RFC3339)+`"}`, agent1); r.code != "conversation.closed" {
		t.Errorf("稍后处理已关闭的会话：%d %s", r.status, r.body)
	}
	if c, _ := do("POST", c3, "reopen", "", agent1); c.Status != "open" {
		t.Errorf("重新打开：%+v", c)
	}
	if ev, _ := lastEvent(c3); ev != "conversation.reopened" {
		t.Errorf("重新打开的系统事件：%s", ev)
	}

	// 稍后处理：时间要晚于现在；手动重新打开记为 unsnoozed
	if _, r := do("POST", c3, "snooze", `{"until":"`+time.Now().Add(-time.Minute).Format(time.RFC3339)+`"}`, agent1); r.code != "conversation.snooze_in_past" || fieldOf(r.body) != "body.until" {
		t.Errorf("过去的时间：%d %s", r.status, r.body)
	}
	until := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if c, r := do("POST", c3, "snooze", `{"until":"`+until.Format(time.RFC3339)+`"}`, agent1); c.Status != "snoozed" || c.SnoozedUntil == nil || !c.SnoozedUntil.Equal(until) {
		t.Fatalf("稍后处理：%d %s", r.status, r.body)
	}
	do("POST", c3, "reopen", "", agent1)
	if ev, _ := lastEvent(c3); ev != "conversation.unsnoozed" {
		t.Errorf("提前打开的系统事件：%s", ev)
	}

	// 稍后处理到期：到时间之前不动，到时间后以系统的身份重新打开
	do("POST", c3, "snooze", `{"until":"`+until.Format(time.RFC3339)+`"}`, agent1)
	expire := conversation.NewExpireSnoozes(env.DB, a.downstream())
	if n, err := expire.Handle(ctx, until.Add(-time.Second)); err != nil || n != 0 {
		t.Fatalf("没到时间：%d %v", n, err)
	}
	if n, err := expire.Handle(ctx, until); err != nil || n != 1 {
		t.Fatalf("到时间：%d %v", n, err)
	}
	if c := get(c3); c.Status != "open" || c.SnoozedUntil != nil {
		t.Errorf("到期后：%+v", c)
	}
	if ev, actor := lastEvent(c3); ev != "conversation.unsnoozed" || actor != "system" {
		t.Errorf("到期的系统事件：%s %s", ev, actor)
	}

	// 优先级
	if c, _ := do("PUT", c3, "priority", `{"priority":"high"}`, agent1); c.Priority != "high" {
		t.Errorf("优先级：%+v", c)
	}
	if ev, _ := lastEvent(c3); ev != "conversation.priority_changed" {
		t.Errorf("优先级的系统事件：%s", ev)
	}
	if _, r := do("PUT", c3, "priority", `{"priority":"urgent"}`, agent1); r.status != http.StatusUnprocessableEntity {
		t.Errorf("不认识的优先级：%d", r.status)
	}

	// 批量：每个会话单独成功或失败；看不到、不存在的会话记为 not_found
	missing := "01900000-0000-7000-8000-0000000000ff"
	r = call(h, "POST", "/t/alpha/api/conversations/bulk/close", `{"ids":["`+c1+`","`+c3+`","`+missing+`"]}`, agent1)
	var results []struct {
		ID   string
		OK   bool
		Code string
	}
	_ = json.Unmarshal([]byte(r.body), &results)
	if r.status != http.StatusOK || len(results) != 3 || !results[0].OK || !results[1].OK || results[2].OK || results[2].Code != "conversation.not_found" {
		t.Fatalf("批量关闭：%d %s", r.status, r.body)
	}
	r = call(h, "POST", "/t/alpha/api/conversations/bulk/assign", `{"ids":["`+c1+`","`+c3+`"],"team_id":"`+teamB+`"}`, agent2)
	_ = json.Unmarshal([]byte(r.body), &results)
	if !results[0].OK || results[1].OK || results[1].Code != "conversation.forbidden" || !strings.Contains(r.body, "没有权限") {
		t.Errorf("坐席批量指派（c1 是他负责的，c3 不是）：%s", r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/conversations/bulk/close", `{"ids":[]}`, owner); r.status != http.StatusUnprocessableEntity {
		t.Errorf("空的批量：%d", r.status)
	}

	// 看不到的会话：坐席不在 B 线，c1 转到 B 线后他不能再操作
	if _, r := do("POST", c1, "reopen", "", agent1); r.code != "conversation.not_found" {
		t.Errorf("看不到的会话：%d %s", r.status, r.body)
	}
}
