package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
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

// TestAutoAssignment 在完整的路由上检查自动指派：轮流分配（依次分给在线的人、跳过离开的、没人在线时留在团队收件箱、
// 手动的团队收件箱不分配）、转到轮流的团队收件箱时分配、回复即领取（备注不领取）、离开时重新分配、负责人离开工作区，
// 以及每次分配在时间线中的系统事件和原因。
func TestAutoAssignment(t *testing.T) {
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
	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
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
	idOf := func(body string) string {
		var v struct{ ID string }
		_ = json.Unmarshal([]byte(body), &v)
		return v.ID
	}
	owner := login("owner@alpha.com")
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	rr := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"轮流","assignment":"round_robin"}`, owner).body)
	manual := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"手动"}`, owner).body)
	ids, cookies := map[string]string{}, map[string]*http.Cookie{}
	for _, key := range []string{"a1", "a2", "a3"} {
		ids[key] = idOf(call(h, "POST", "/t/alpha/api/members", `{"email":"`+key+`@alpha.com","name":"`+key+`","password":"password","role_id":"`+agentRole+`"}`, owner).body)
		cookies[key] = login(key + "@alpha.com")
		call(h, "PUT", "/t/alpha/api/teams/"+rr+"/members/"+ids[key], "", owner)
		call(h, "PUT", "/t/alpha/api/teams/"+manual+"/members/"+ids[key], "", owner)
	}
	setStatus := func(key, body string) {
		t.Helper()
		if r := call(h, "PUT", "/t/alpha/api/me/status", body, cookies[key]); r.status != http.StatusOK {
			t.Fatalf("%s 的在线状态：%d %s", key, r.status, r.body)
		}
	}
	online, away := `{"status":"online","reassign":false}`, `{"status":"away","reassign":false}`
	setStatus("a1", online)
	setStatus("a2", online) // a3 保持默认的离开

	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	channelTo := func(teamID string) string {
		t.Helper()
		ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: teamID, TeamID: &teamID})
		if err != nil {
			t.Fatal(err)
		}
		return ch.ID
	}
	rrChannel, manualChannel := channelTo(rr), channelTo(manual)
	receive := conversation.NewReceive(env.DB, a.channels, a.downstream())
	send := func(channelID, from string) string {
		t.Helper()
		res, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: channelID, From: from, Text: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		return res.ConversationID
	}
	assignee := func(id string) string {
		t.Helper()
		var c struct{ Assignee *struct{ ID string } }
		_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/conversations/"+id, "", owner).body), &c)
		if c.Assignee == nil {
			return ""
		}
		return c.Assignee.ID
	}
	// reasons 返回会话时间线中 conversation.assigned 系统事件的原因和发起者的类型，按顺序
	reasons := func(id string) []string {
		t.Helper()
		var items []struct {
			Actor   struct{ Type string }
			Content struct {
				Event string
				Data  struct{ Reason string }
			}
		}
		_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/conversations/"+id+"/items", "", owner).body), &items)
		var out []string
		for _, it := range items {
			if it.Content.Event == "conversation.assigned" {
				out = append(out, it.Actor.Type+":"+it.Content.Data.Reason)
			}
		}
		return out
	}

	// 轮流分配：依次分给在线的 a1、a2，跳过离开的 a3，第三个会话回到第一位
	c1, c2, c3 := send(rrChannel, "u1"), send(rrChannel, "u2"), send(rrChannel, "u3")
	got := []string{assignee(c1), assignee(c2), assignee(c3)}
	if got[0] == got[1] || got[2] != got[0] || !slices.Contains([]string{ids["a1"], ids["a2"]}, got[0]) || !slices.Contains([]string{ids["a1"], ids["a2"]}, got[1]) {
		t.Fatalf("轮流分配：%v（a1 %s，a2 %s）", got, ids["a1"], ids["a2"])
	}
	if r := reasons(c1); !slices.Equal(r, []string{"system:round_robin"}) {
		t.Errorf("轮流分配的系统事件：%v", r)
	}
	// 手动的团队收件箱不分配
	m1 := send(manualChannel, "u4")
	if assignee(m1) != "" {
		t.Errorf("手动的团队收件箱分配了：%s", assignee(m1))
	}

	// 回复即领取：内部备注不领取，回复时领取，团队收件箱不变
	call(h, "POST", "/t/alpha/api/conversations/"+m1+"/notes", `{"text":"看看"}`, cookies["a3"])
	if assignee(m1) != "" {
		t.Errorf("内部备注领取了会话")
	}
	call(h, "POST", "/t/alpha/api/conversations/"+m1+"/replies", `{"text":"您好"}`, cookies["a3"])
	if assignee(m1) != ids["a3"] || !slices.Equal(reasons(m1), []string{"member:reply"}) {
		t.Errorf("回复即领取：%s %v", assignee(m1), reasons(m1))
	}
	// 已经有负责人时，别人的回复不改变负责人
	call(h, "POST", "/t/alpha/api/conversations/"+m1+"/replies", `{"text":"补充一下"}`, cookies["a1"])
	if assignee(m1) != ids["a3"] {
		t.Errorf("别人回复后负责人变了：%s", assignee(m1))
	}

	// 转到轮流的团队收件箱：清空负责人后分给在线的人；系统事件是手动的转出和系统的分配
	if r := call(h, "PUT", "/t/alpha/api/conversations/"+m1+"/assignment", `{"team_id":"`+rr+`"}`, owner); r.status != http.StatusOK {
		t.Fatalf("转到轮流的团队收件箱：%d %s", r.status, r.body)
	}
	if !slices.Contains([]string{ids["a1"], ids["a2"]}, assignee(m1)) || !slices.Equal(reasons(m1), []string{"member:reply", "member:manual", "system:round_robin"}) {
		t.Errorf("转入后：%s %v", assignee(m1), reasons(m1))
	}

	// 离开时重新分配：c1 的负责人离开并选了重新分配，对方再发来消息时会话放回团队收件箱，分给另一位在线的人
	holder := assignee(c1)
	other := map[string]string{ids["a1"]: "a2", ids["a2"]: "a1"}[holder]
	holderKey := map[string]string{ids["a1"]: "a1", ids["a2"]: "a2"}[holder]
	setStatus(holderKey, `{"status":"away","reassign":true}`)
	send(rrChannel, "u1")
	if assignee(c1) != ids[other] || !slices.Equal(reasons(c1), []string{"system:round_robin", "system:away", "system:round_robin"}) {
		t.Errorf("离开时重新分配：%s %v", assignee(c1), reasons(c1))
	}
	// 离开但没有选重新分配：负责人不变
	setStatus(other, away)
	send(rrChannel, "u1")
	if assignee(c1) != ids[other] {
		t.Errorf("没有选重新分配时负责人变了：%s", assignee(c1))
	}

	// 没有人在线：新会话留在团队收件箱
	if c := send(rrChannel, "u5"); assignee(c) != "" {
		t.Errorf("没人在线时分配了：%s", assignee(c))
	}

	// 负责人离开工作区：他负责的未关闭会话放回团队收件箱并重新分配，已关闭的保留原来的负责人
	setStatus("a3", online)
	leaving := assignee(c2)
	var leavingClosed string
	for _, id := range []string{c1, c3, m1} {
		if assignee(id) == leaving {
			leavingClosed = id
			break
		}
	}
	if leavingClosed != "" {
		call(h, "POST", "/t/alpha/api/conversations/"+leavingClosed+"/close", "", owner)
	}
	if r := call(h, "POST", "/t/alpha/api/members/"+leaving+"/remove", "", owner); r.status != http.StatusNoContent {
		t.Fatalf("移出成员：%d %s", r.status, r.body)
	}
	if assignee(c2) != ids["a3"] || !slices.Equal(reasons(c2)[1:], []string{"system:member_left", "system:round_robin"}) {
		t.Errorf("负责人离开工作区：%s %v", assignee(c2), reasons(c2))
	}
	if leavingClosed != "" && assignee(leavingClosed) != leaving {
		t.Errorf("已关闭的会话换了负责人")
	}
}
