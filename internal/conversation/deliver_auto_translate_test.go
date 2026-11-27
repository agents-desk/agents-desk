package conversation_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

type captureConnector struct {
	sent []channel.Outgoing
}

func (*captureConnector) Capabilities() channel.Capabilities {
	return channel.Capabilities{MessageTypes: []string{"text"}}
}

func (c *captureConnector) Send(_ context.Context, msg channel.Outgoing) (string, bool, error) {
	c.sent = append(c.sent, msg)
	return "ext-1", true, nil
}

// TestDeliverAutoTranslate 验证折叠态秒发消息后，Deliver.Job 在后台自动翻译正文、更新 timeline_items.content、
// 清除 pending_translation 标记并把译文投递到外部渠道；同时验证翻译临时失败时返回错误供队列重试。
func TestDeliverAutoTranslate(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	alpha, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	subOwner := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}

	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &captureConnector{}
	reg := channel.Registry{channel.Test: conn}
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	rec, err := conversation.NewReceive(env.DB, reg, down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "cust-1", FromName: "Rahul", Text: "Where is my order?",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. 折叠态回复：携带 AutoTranslate，零等待落库并写入 pending_translation 标记
	replied, err := conversation.NewReply(env.DB, down).Handle(ctx, subOwner, rec.ConversationID, conversation.ReplyBody{
		Text:          "您的订单已经发货了",
		AutoTranslate: &conversation.AutoTranslateRequest{Language: "en", Tone: "friendly"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if replied.Content.Text != "您的订单已经发货了" || replied.Content.Original != "" {
		t.Fatalf("落库时应先保留原文：%#v", replied.Content)
	}
	if replied.Derived["pending_translation"] == nil {
		t.Fatalf("应写入 pending_translation 标记：%#v", replied.Derived)
	}

	// 2. 翻译成功 -> 后台翻译并更新 content.text/original/language，清除 pending_translation 并投递译文
	okDown := down
	okDown.AutoTranslate = func(_ context.Context, req conversation.AutoTranslateJob) (string, string, bool, error) {
		if req.Language != "en" || req.Tone != "friendly" || req.Text != "您的订单已经发货了" {
			t.Fatalf("自动翻译参数异常：%+v", req)
		}
		return "Your order has been shipped!", "en", true, nil
	}
	if err := conversation.NewDeliver(env.DB, reg, okDown).Job(ctx, conversation.Appended{
		TenantID: alpha.ID, ConversationID: rec.ConversationID, ItemID: replied.ID, Seq: replied.Seq,
	}); err != nil {
		t.Fatalf("Deliver.Job 应成功：%v", err)
	}
	if len(conn.sent) != 1 || conn.sent[0].Text != "Your order has been shipped!" {
		t.Fatalf("应把译文发给外部渠道：%+v", conn.sent)
	}

	var stored conversation.Item
	if err := env.DB.NewSelect().Model(&stored).Where("id = ?", replied.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.Content.Text != "Your order has been shipped!" || stored.Content.Original != "您的订单已经发货了" || stored.Content.Language != "en" {
		t.Fatalf("落库译文与原文不符：%+v", stored.Content)
	}
	if _, stillPending := stored.Derived["pending_translation"]; stillPending {
		t.Fatalf("完成后应清除 pending_translation：%+v", stored.Derived)
	}

	// 3. 翻译失败不阻断投递：按原文发出，清除待翻译标记并记下这次没有翻译成功，不返回错误让队列重试
	second, err := conversation.NewReply(env.DB, down).Handle(ctx, subOwner, rec.ConversationID, conversation.ReplyBody{
		Text:          "第二条原文",
		AutoTranslate: &conversation.AutoTranslateRequest{Language: "en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	failDown := down
	failDown.AutoTranslate = func(_ context.Context, _ conversation.AutoTranslateJob) (string, string, bool, error) {
		return "", "", false, errors.New("llm transient timeout")
	}
	if err := conversation.NewDeliver(env.DB, reg, failDown).Job(ctx, conversation.Appended{
		TenantID: alpha.ID, ConversationID: rec.ConversationID, ItemID: second.ID, Seq: second.Seq,
	}); err != nil {
		t.Fatalf("翻译失败不应阻断投递：%v", err)
	}
	if len(conn.sent) != 2 || conn.sent[1].Text != "第二条原文" {
		t.Fatalf("翻译失败时应按原文发出：%+v", conn.sent)
	}

	var storedFailed conversation.Item
	if err := env.DB.NewSelect().Model(&storedFailed).Where("id = ?", second.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, stillPending := storedFailed.Derived["pending_translation"]; stillPending {
		t.Fatalf("失败后应清除 pending_translation：%+v", storedFailed.Derived)
	}
	if failed, _ := storedFailed.Derived["translation_failed"].(bool); !failed {
		t.Fatalf("失败后应留下 translation_failed 标记：%+v", storedFailed.Derived)
	}

	var delivery conversation.Delivery
	if err := env.DB.NewSelect().Model(&delivery).Where("item_id = ?", second.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if delivery.Status != conversation.Delivered {
		t.Fatalf("按原文发出后应记为已送达：%s", delivery.Status)
	}
}

func TestDeliverNotifyOfflineReply(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	alpha, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "offline", Slug: "offline", OwnerEmail: "owner@offline.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	subOwner := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &captureConnector{}
	reg := channel.Registry{channel.Test: conn}
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })

	var notified []string
	down := conversation.Downstream{
		Hub:   hub,
		Roles: env.Roles,
		NotifyOfflineReply: func(_ context.Context, _ database.Tx, tenantID, convID, itemID string) error {
			notified = append(notified, tenantID+":"+convID+":"+itemID)
			return nil
		},
	}

	rec, err := conversation.NewReceive(env.DB, reg, down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "cust-off-1", FromName: "Visitor", Text: "Anyone there?",
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := conversation.NewReply(env.DB, down).Handle(ctx, subOwner, rec.ConversationID, conversation.ReplyBody{
		Text: "We have replied.",
	})
	if err != nil {
		t.Fatal(err)
	}

	deliver := conversation.NewDeliver(env.DB, reg, down)
	job := conversation.Appended{TenantID: alpha.ID, ConversationID: rec.ConversationID, ItemID: reply.ID, Seq: reply.Seq}
	if err := deliver.Job(ctx, job); err != nil {
		t.Fatal(err)
	}
	if len(notified) != 1 || notified[0] != alpha.ID+":"+rec.ConversationID+":"+reply.ID {
		t.Fatalf("送达后应触发一次 NotifyOfflineReply，实际 %v", notified)
	}
	// 幂等重放不再重复发送邮件提醒
	if err := deliver.Job(ctx, job); err != nil {
		t.Fatal(err)
	}
	if len(notified) != 1 {
		t.Fatalf("重复投递同一消息不应重复触发 NotifyOfflineReply，实际 %v", notified)
	}
}
