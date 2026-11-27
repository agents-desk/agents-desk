package conversation_test

import (
	"context"
	"sync"
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

	// 3. 翻译失败应阻断原文外发，返回 ErrAutoTranslateFailed 供队列重试；进入死信后标记 translation_failed 与 DeliveryError
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
	failDeliver := conversation.NewDeliver(env.DB, reg, failDown)
	job2 := conversation.Appended{TenantID: alpha.ID, ConversationID: rec.ConversationID, ItemID: second.ID, Seq: second.Seq}
	err = failDeliver.Job(ctx, job2)
	if !errors.Is(err, conversation.ErrAutoTranslateFailed) {
		t.Fatalf("翻译失败应返回 ErrAutoTranslateFailed 交给队列重试，实际 %v", err)
	}
	if len(conn.sent) != 1 {
		t.Fatalf("翻译失败时绝不应将坐席原文发往外部渠道，实际发送数=%d", len(conn.sent))
	}
	if err := failDeliver.OnDead(ctx, job2, err); err != nil {
		t.Fatal(err)
	}

	var storedFailed conversation.Item
	if err := env.DB.NewSelect().Model(&storedFailed).Where("id = ?", second.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if storedFailed.Derived["pending_translation"] == nil {
		t.Fatalf("进入死信后应保留 pending_translation 以便重试翻译：%+v", storedFailed.Derived)
	}
	if failed, _ := storedFailed.Derived["translation_failed"].(bool); !failed {
		t.Fatalf("进入死信后应留下 translation_failed 标记：%+v", storedFailed.Derived)
	}
	tlItems, err := conversation.NewTimeline(env.DB).Handle(ctx, subOwner, rec.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var dtoSecond *conversation.ItemDTO
	for i := range tlItems {
		if tlItems[i].ID == second.ID {
			dtoSecond = &tlItems[i]
		}
	}
	if dtoSecond == nil || dtoSecond.Delivery == nil || *dtoSecond.Delivery != conversation.Failed || dtoSecond.DeliveryError == "" {
		t.Fatalf("时间线项应包含失败状态与失败原因，实际 %+v", dtoSecond)
	}

	// 4. 默认 Retry 重试翻译并在翻译恢复后成功发出译文；SkipTranslation=true 则跳过翻译直接按原文发出
	retryOKDown := down
	retryOKDown.AutoTranslate = func(_ context.Context, req conversation.AutoTranslateJob) (string, string, bool, error) {
		if req.Text != "第二条原文" || req.Language != "en" {
			t.Fatalf("重试翻译参数异常：%+v", req)
		}
		return "Second message translated", "en", true, nil
	}
	retriedTranslate, err := conversation.NewRetry(env.DB, retryOKDown).Handle(ctx, subOwner, rec.ConversationID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retriedTranslate.Derived["translation_failed"] != nil || retriedTranslate.Derived["pending_translation"] == nil {
		t.Fatalf("重试翻译应清除 translation_failed 并保留 pending_translation：%+v", retriedTranslate.Derived)
	}
	if err := conversation.NewDeliver(env.DB, reg, retryOKDown).Job(ctx, job2); err != nil {
		t.Fatal(err)
	}
	if len(conn.sent) != 2 || conn.sent[1].Text != "Second message translated" {
		t.Fatalf("重试翻译后应发出译文：%+v", conn.sent)
	}

	third, err := conversation.NewReply(env.DB, down).Handle(ctx, subOwner, rec.ConversationID, conversation.ReplyBody{
		Text:          "第三条按原文补发",
		AutoTranslate: &conversation.AutoTranslateRequest{Language: "en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	job3 := conversation.Appended{TenantID: alpha.ID, ConversationID: rec.ConversationID, ItemID: third.ID, Seq: third.Seq}
	err = failDeliver.Job(ctx, job3)
	if !errors.Is(err, conversation.ErrAutoTranslateFailed) {
		t.Fatalf("预期 ErrAutoTranslateFailed，实际 %v", err)
	}
	if err := failDeliver.OnDead(ctx, job3, err); err != nil {
		t.Fatal(err)
	}
	retriedOrig, err := conversation.NewRetry(env.DB, failDown).Handle(ctx, subOwner, rec.ConversationID, third.ID, conversation.RetryBody{SkipTranslation: true})
	if err != nil {
		t.Fatal(err)
	}
	if retriedOrig.Derived["pending_translation"] != nil || retriedOrig.Derived["translation_failed"] != nil {
		t.Fatalf("按原文重试应同时清除 pending_translation 与 translation_failed：%+v", retriedOrig.Derived)
	}
	if err := failDeliver.Job(ctx, job3); err != nil {
		t.Fatal(err)
	}
	if len(conn.sent) != 3 || conn.sent[2].Text != "第三条按原文补发" {
		t.Fatalf("按原文重试应直接发出原文：%+v", conn.sent)
	}
}

func TestDeliverAndSaveTranslationNoLostDerivedUpdate(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	alpha, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "race", Slug: "race", OwnerEmail: "owner@race.test", OwnerName: "Owner", OwnerPassword: "password",
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
	down := conversation.Downstream{Roles: env.Roles}
	rec, err := conversation.NewReceive(env.DB, reg, down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "cust-race", FromName: "Visitor", Text: "Need order help",
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := conversation.NewReply(env.DB, down).Handle(ctx, subOwner, rec.ConversationID, conversation.ReplyBody{
		Text:          "正在为您核实物流单号",
		AutoTranslate: &conversation.AutoTranslateRequest{Language: "en"},
	})
	if err != nil {
		t.Fatal(err)
	}

	okDown := down
	okDown.AutoTranslate = func(_ context.Context, _ conversation.AutoTranslateJob) (string, string, bool, error) {
		return "Checking your tracking number", "en", true, nil
	}
	deliver := conversation.NewDeliver(env.DB, reg, okDown)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- deliver.Job(ctx, conversation.Appended{TenantID: alpha.ID, ConversationID: rec.ConversationID, ItemID: reply.ID, Seq: reply.Seq})
	}()
	go func() {
		defer wg.Done()
		errCh <- conversation.SaveTranslation(ctx, env.DB, down, alpha.ID, reply.ID, conversation.Translated{
			Language:     "zh-CN",
			Translations: map[string]string{"ja": "追跡番号を確認しています"},
		})
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("并发更新 derived 不应失败：%v", err)
		}
	}

	var stored conversation.Item
	if err := env.DB.NewSelect().Model(&stored).Where("id = ?", reply.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.Derived["pending_translation"] != nil {
		t.Fatalf("pending_translation 不应被并发 SaveTranslation 恢复：%+v", stored.Derived)
	}
	if stored.Derived["language"] != "zh-CN" {
		t.Fatalf("SaveTranslation 写入的 language 不应丢失：%+v", stored.Derived)
	}
	trs, _ := stored.Derived["translations"].(map[string]any)
	if trs["ja"] != "追跡番号を確認しています" {
		t.Fatalf("SaveTranslation 写入的译文不应丢失：%+v", stored.Derived)
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
