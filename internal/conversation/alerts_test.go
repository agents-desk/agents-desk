package conversation_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
	"agents-desk/internal/role"
	"agents-desk/internal/tenant"
)

// readAlert 从广播订阅 sub 读取一条推送事件。
func readAlert(t *testing.T, sub *broadcast.Subscription) push.Event {
	t.Helper()
	select {
	case msg := <-sub.C():
		var ev push.Event
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			t.Fatal(err)
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("等待实时推送事件超时")
		return push.Event{}
	}
}

// TestPushAlerts 验证三类实时提醒信号（assigned / new_message / mention）仅推送给目标成员，
// 成员自己发起的操作不提醒本人，且无权看到该会话的其他成员不收到提醒。
func TestPushAlerts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	hub := broadcast.New(broadcasttest.NewBackend(), nil)
	t.Cleanup(func() { _ = hub.Close() })

	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alerts", Slug: "alerts", OwnerEmail: "owner@alerts.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerSub := identity.Subject{
		Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	agentRole, err := role.NewCreate(env.DB).Handle(ctx, ownerSub, role.Params{
		Name:              "测试坐席",
		ConversationScope: auth.ScopeTeam,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentRoleID := agentRole.ID
	add := member.NewAdd(env.DB, env.MemberLogins, env.Files)
	a1, err := add.Handle(ctx, ownerSub, member.AddParams{Email: "a1@alerts.com", Name: "坐席一", Password: "password", RoleID: agentRoleID})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := add.Handle(ctx, ownerSub, member.AddParams{Email: "a2@alerts.com", Name: "坐席二", Password: "password", RoleID: agentRoleID})
	if err != nil {
		t.Fatal(err)
	}
	a1Sub := identity.Subject{Type: member.Type, ID: a1.ID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeTeam, nil))}
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	ownerChan := hub.Subscribe(push.Channel(tn.ID, tn.OwnerID))
	defer ownerChan.Close()
	a1Chan := hub.Subscribe(push.Channel(tn.ID, a1.ID))
	defer a1Chan.Close()
	a2Chan := hub.Subscribe(push.Channel(tn.ID, a2.ID))
	defer a2Chan.Close()

	// 1. 客户发来首条消息（尚无负责人）：owner 收到刷新但无 alert
	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	got, err := recv.Handle(ctx, system, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c1", FromName: "客户", Text: "hello", ExternalID: "m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev := readAlert(t, ownerChan); ev.Alert != "" {
		t.Fatalf("未指派的新会话不应提醒 owner：%+v", ev)
	}

	// 2. owner 把会话指派给 a1：a1 收到 assigned 提醒，owner 收到刷新但无 alert
	if _, err := conversation.NewAssign(env.DB, down).Handle(ctx, ownerSub, got.ConversationID, conversation.Assignment{AssigneeID: &a1.ID}); err != nil {
		t.Fatal(err)
	}
	if ev := readAlert(t, ownerChan); ev.Alert != "" {
		t.Fatalf("发起指派的 owner 不应收到提醒：%+v", ev)
	}
	if ev := readAlert(t, a1Chan); ev.Alert != push.AlertAssigned {
		t.Fatalf("被指派的 a1 应收到 assigned 提醒，实际：%+v", ev)
	}

	// 3. 客户再发新消息：负责人 a1 收到 new_message 提醒，owner 无 alert
	if _, err := recv.Handle(ctx, system, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c1", FromName: "客户", Text: "are you there?", ExternalID: "m2",
	}); err != nil {
		t.Fatal(err)
	}
	if ev := readAlert(t, ownerChan); ev.Alert != "" {
		t.Fatalf("非负责人的 owner 不应收到 new_message 提醒：%+v", ev)
	}
	if ev := readAlert(t, a1Chan); ev.Alert != push.AlertNewMessage {
		t.Fatalf("负责人 a1 应收到 new_message 提醒，实际：%+v", ev)
	}

	// 4. a1 在内部备注中 @ a2（也包含自己）：a2 收到 mention 提醒，a1 和 owner 无 alert
	if _, err := conversation.NewAddNote(env.DB, down).Handle(ctx, a1Sub, got.ConversationID, "请 @坐席二 看下", []string{a2.ID, a1.ID}); err != nil {
		t.Fatal(err)
	}
	if ev := readAlert(t, ownerChan); ev.Alert != "" {
		t.Fatalf("未被 @ 的 owner 不应收到 mention 提醒：%+v", ev)
	}
	if ev := readAlert(t, a1Chan); ev.Alert != "" {
		t.Fatalf("备注发起人 a1 不应收到自己的 mention 提醒：%+v", ev)
	}
	if ev := readAlert(t, a2Chan); ev.Alert != push.AlertMention {
		t.Fatalf("被 @ 的 a2 应收到 mention 提醒，实际：%+v", ev)
	}

	// 5. a1 自己回复（即使原来没指派，主动领取）不触发 assigned 提醒
	got2, err := recv.Handle(ctx, system, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c2", FromName: "客户二", Text: "hi", ExternalID: "m3",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = readAlert(t, ownerChan)
	if _, err := conversation.NewAssign(env.DB, down).Handle(ctx, ownerSub, got2.ConversationID, conversation.Assignment{AssigneeID: &tn.OwnerID}); err != nil {
		t.Fatal(err)
	}
	if ev := readAlert(t, ownerChan); ev.Alert != "" {
		t.Fatalf("自己领取/指派给自己不应收到 assigned 提醒：%+v", ev)
	}
}
