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

// TestConversations 在完整的路由上检查会话的接口：成员登录后读取会话列表和时间线、回复、写内部备注，
// 请求体的校验，以及没有登录时被拒绝。
func TestConversations(t *testing.T) {
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
	owner := login("owner@alpha.com")
	var agentMember, team struct{ ID string }
	_ = json.Unmarshal([]byte(call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"坐席","password":"password","role_id":"`+roleID(t, h, "alpha", "坐席", owner)+`"}`, owner).body), &agentMember)
	agent := login("agent@alpha.com")
	// 坐席能看到所在团队收件箱的会话：测试渠道的新会话进入他所在的团队收件箱
	_ = json.Unmarshal([]byte(call(h, "POST", "/t/alpha/api/teams", `{"name":"一线"}`, owner).body), &team)
	call(h, "PUT", "/t/alpha/api/teams/"+team.ID+"/members/"+agentMember.ID, "", owner)

	// 客户经测试渠道发来一条消息
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试", TeamID: &team.ID})
	if err != nil {
		t.Fatal(err)
	}
	received, err := conversation.NewReceive(env.DB, a.channels, a.downstream()).Handle(ctx, system, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "rahul", FromName: "Rahul", Text: "mera order nahi aaya",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := received.ConversationID

	// 坐席读取会话列表、会话和时间线
	r := call(h, "GET", "/t/alpha/api/conversations?status=open", "", agent)
	if r.status != http.StatusOK || !strings.Contains(r.body, id) || !strings.Contains(r.body, `"preview":"mera order nahi aaya"`) {
		t.Fatalf("会话列表：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/conversations/"+id, "", agent); r.status != http.StatusOK || !strings.Contains(r.body, `"name":"Rahul"`) {
		t.Fatalf("读取会话：%d %s", r.status, r.body)
	}

	// 回复和内部备注
	if r := call(h, "POST", "/t/alpha/api/conversations/"+id+"/replies", `{"text":"您好"}`, agent); r.status != http.StatusCreated || !strings.Contains(r.body, `"delivery":"sending"`) {
		t.Fatalf("回复：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/conversations/"+id+"/notes", `{"text":"内部备注"}`, agent); r.status != http.StatusCreated || !strings.Contains(r.body, `"visibility":"members"`) {
		t.Fatalf("内部备注：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/conversations/"+id+"/replies", `{"text":""}`, agent); fieldOf(r.body) != "body.text" {
		t.Errorf("空回复：%d %s", r.status, r.body)
	}
	// 增量读取：序号 1 之后是回复即领取的系统事件、回复和备注
	r = call(h, "GET", "/t/alpha/api/conversations/"+id+"/items?after=1", "", agent)
	var items []struct{ Seq int64 }
	if err := json.Unmarshal([]byte(r.body), &items); err != nil || len(items) != 3 || items[0].Seq != 2 {
		t.Fatalf("增量读取时间线：%d %s", r.status, r.body)
	}

	// 没有登录
	if r := call(h, "GET", "/t/alpha/api/conversations", "", nil); r.status != http.StatusUnauthorized {
		t.Errorf("没有登录：%d", r.status)
	}
}
