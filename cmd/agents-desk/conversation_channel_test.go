package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/realtime/realtimetest"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestConversationChannel 检查会话的频道：能看到会话的成员可以加入（主管理员看得到全部会话），看不到的坐席被拒绝，
// 别的工作区的频道也被拒绝；客户在聊天窗口中正在输入时，加入了频道的成员收到 typing。
func TestConversationChannel(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	hub := broadcast.New(broadcasttest.NewBackend(), nil)
	t.Cleanup(func() { _ = hub.Close() })
	a := &App{
		cfg: config.Default[Config](), db: env.DB, hub: hub, roles: env.Roles, presence: realtimetest.NewPresence(),
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, localDisk: env.Disk, channels: channel.DefaultRegistry(),
	}
	rt, err := a.newRealtime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	h := a.routes(nil)
	h.With(tenant.Middleware(env.DB)).Handle("/t/{slug}/realtime", rt.Handler())
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
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
	// 坐席只看得到所在团队收件箱的和指派给自己的会话；他不在任何团队收件箱中
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	if r := call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"agent","password":"password","role_id":"`+agentRole+`"}`, owner); r.status != http.StatusCreated {
		t.Fatalf("添加坐席：%d %s", r.status, r.body)
	}
	agent := login("agent@alpha.com")

	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}
	got, err := conversation.NewReceive(env.DB, a.channels, down).Handle(ctx, system, conversation.ReceiveParams{ChannelID: ch.ID, From: "rahul", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	topic := conversation.Channel(tn.ID, got.ConversationID)

	ownerWS := dialRealtime(t, srv.URL+"/t/alpha/realtime", owner)
	if code := ownerWS.subscribe(topic); code != 0 {
		t.Fatalf("主管理员加入会话的频道：%d", code)
	}
	agentWS := dialRealtime(t, srv.URL+"/t/alpha/realtime", agent)
	for name, ch := range map[string]string{
		"看不到的会话":   topic,
		"不存在的会话":   conversation.Channel(tn.ID, "01900000-0000-7000-8000-000000000000"),
		"别的工作区的频道": conversation.Channel("01900000-0000-7000-8000-0000000000bb", got.ConversationID),
	} {
		if code := agentWS.subscribe(ch); code != 103 {
			t.Errorf("坐席订阅%s：%d，应当是 103", name, code)
		}
	}

	// 客户正在输入：推送到会话的频道
	var contactID string
	if err := env.DB.NewRaw("SELECT contact_id FROM conversations WHERE id = ?", got.ConversationID).Scan(ctx, &contactID); err != nil {
		t.Fatal(err)
	}
	visitor := identity.Subject{Type: contact.Type, ID: contactID, Tenant: tn.ID}
	if err := conversation.NewTypingByContact(env.DB, down).Handle(ctx, visitor, ch.ID, got.ConversationID); err != nil {
		t.Fatal(err)
	}
	if ev := ownerWS.publication(); ev.Type != "typing" {
		t.Fatalf("正在输入：%+v", ev)
	}
}
