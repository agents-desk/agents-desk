package automation_test

import (
	"context"
	"strings"
	"testing"

	"agents-desk/internal/apptest"
	"agents-desk/internal/automation"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/tickettype"
)

// TestTicketStatusNotifyAutomation 验证内置自动化“工单状态变化时通知关联客户”：
//   - 客户可见工单切状态时，所有关联服务型会话按各自客户语言收到结构化状态卡片与本地化文本；
//   - 仅成员可见工单切状态不通知客户；
//   - 客户发消息触发 waiting_on_customer → in_progress 回流时不回发多余状态通知；
//   - 管理员关闭该规则开关后停止通知。
func TestTicketStatusNotifyAutomation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "tkauto")

	ch, err := channel.NewCreate(env.DB).Handle(ctx, f.system, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	recv := conversation.NewReceive(env.DB, f.reg, f.down)
	cEn, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{ChannelID: ch.ID, From: "en-user", FromName: "Rahul", Text: "help"})
	if err != nil {
		t.Fatal(err)
	}
	cZh, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{ChannelID: ch.ID, From: "zh-user", FromName: "张三", Text: "你好"})
	if err != nil {
		t.Fatal(err)
	}
	setLang := conversation.NewSetLanguage(env.DB, f.down)
	if _, err := setLang.Handle(ctx, f.owner, cEn.ConversationID, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := setLang.Handle(ctx, f.owner, cZh.ConversationID, "zh-CN"); err != nil {
		t.Fatal(err)
	}

	ttPub, err := tickettype.NewCreate(env.DB).Handle(ctx, f.system, tickettype.CreateParams{Name: "充值问题", DefaultVisibility: tickettype.VisibilityAll})
	if err != nil {
		t.Fatal(err)
	}
	ttPriv, err := tickettype.NewCreate(env.DB).Handle(ctx, f.system, tickettype.CreateParams{Name: "内部审批", DefaultVisibility: tickettype.VisibilityMembers})
	if err != nil {
		t.Fatal(err)
	}

	createTicket := conversation.NewCreateTicket(env.DB, f.down)
	setStatus := conversation.NewSetTicketStatus(env.DB, f.down)
	tl := conversation.NewTimeline(env.DB)

	tkPub, err := createTicket.Handle(ctx, f.owner, conversation.CreateTicketParams{
		TypeID:               &ttPub.ID,
		Title:                "UPI 掉单",
		SourceConversationID: &cEn.ConversationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewLinkTicketContact(env.DB, f.down).Handle(ctx, f.owner, tkPub.ID, conversation.LinkTicketContactBody{ConversationID: &cZh.ConversationID}); err != nil {
		t.Fatal(err)
	}
	tkPriv, err := createTicket.Handle(ctx, f.owner, conversation.CreateTicketParams{
		TypeID:               &ttPriv.ID,
		Title:                "财务内部复核",
		SourceConversationID: &cEn.ConversationID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. 客户可见工单切到 waiting_on_customer → 触发自动化通知两个关联会话
	if _, err := setStatus.Handle(ctx, f.owner, tkPub.ID, conversation.TicketWaiting); err != nil {
		t.Fatal(err)
	}
	pubItems, err := tl.Handle(ctx, f.owner, tkPub.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	lastEv := pubItems[len(pubItems)-1]
	f.runItem(t, tkPub.ID, lastEv.ID, lastEv.Seq)

	// 英文客户收到英文状态文本，中文客户收到中文状态文本，均带 ticket_status_changed 结构化 Prompt
	enItems, _ := tl.Handle(ctx, f.owner, cEn.ConversationID, 0, 50)
	zhItems, _ := tl.Handle(ctx, f.owner, cZh.ConversationID, 0, 50)
	var enNotif, zhNotif *conversation.ItemDTO
	for i := range enItems {
		if enItems[i].Content.Prompt != nil && enItems[i].Content.Prompt.Kind == "ticket_status_changed" {
			enNotif = &enItems[i]
		}
	}
	for i := range zhItems {
		if zhItems[i].Content.Prompt != nil && zhItems[i].Content.Prompt.Kind == "ticket_status_changed" {
			zhNotif = &zhItems[i]
		}
	}
	if enNotif == nil || !strings.Contains(enNotif.Content.Text, "Waiting for your reply") {
		t.Fatalf("英文会话应收到英文状态通知：%+v", enNotif)
	}
	if zhNotif == nil || !strings.Contains(zhNotif.Content.Text, "等待您补充信息") {
		t.Fatalf("中文会话应收到中文状态通知：%+v", zhNotif)
	}

	// 2. 客户回复自动把工单切回 in_progress，执行自动化时不应额外发通知
	if _, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{ChannelID: ch.ID, From: "en-user", Text: "screenshot attached"}); err != nil {
		t.Fatal(err)
	}
	pubItems2, _ := tl.Handle(ctx, f.owner, tkPub.ID, 0, 50)
	autoEv := pubItems2[len(pubItems2)-1]
	f.runItem(t, tkPub.ID, autoEv.ID, autoEv.Seq)
	enAfterReply, _ := tl.Handle(ctx, f.owner, cEn.ConversationID, 0, 50)
	notifCount := 0
	for _, it := range enAfterReply {
		if it.Content.Prompt != nil && it.Content.Prompt.Kind == "ticket_status_changed" {
			notifCount++
		}
	}
	if notifCount != 1 {
		t.Fatalf("客户回流触发的状态变更不应再次发通知，当前通知数=%d", notifCount)
	}

	// 3. 仅成员可见工单切换状态，绝不通知客户
	if _, err := setStatus.Handle(ctx, f.owner, tkPriv.ID, conversation.TicketResolved); err != nil {
		t.Fatal(err)
	}
	privItems, _ := tl.Handle(ctx, f.owner, tkPriv.ID, 0, 50)
	privEv := privItems[len(privItems)-1]
	f.runItem(t, tkPriv.ID, privEv.ID, privEv.Seq)
	enAfterPriv, _ := tl.Handle(ctx, f.owner, cEn.ConversationID, 0, 50)
	notifCount = 0
	for _, it := range enAfterPriv {
		if it.Content.Prompt != nil && it.Content.Prompt.Kind == "ticket_status_changed" {
			notifCount++
		}
	}
	if notifCount != 1 {
		t.Fatalf("仅成员可见工单不应通知客户，当前通知数=%d", notifCount)
	}

	// 4. 管理员关闭内置自动化开关后，公开工单切到 resolved 不再发送通知
	rules, err := automation.NewList(env.DB).Handle(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.BuiltinKey == automation.BuiltinTicketStatusNotify {
			if _, err := automation.NewUpdate(env.DB).Handle(ctx, f.owner, r.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := setStatus.Handle(ctx, f.owner, tkPub.ID, conversation.TicketResolved); err != nil {
		t.Fatal(err)
	}
	pubItems3, _ := tl.Handle(ctx, f.owner, tkPub.ID, 0, 50)
	resolvedEv := pubItems3[len(pubItems3)-1]
	f.runItem(t, tkPub.ID, resolvedEv.ID, resolvedEv.Seq)
	enAfterOff, _ := tl.Handle(ctx, f.owner, cEn.ConversationID, 0, 50)
	notifCount = 0
	for _, it := range enAfterOff {
		if it.Content.Prompt != nil && it.Content.Prompt.Kind == "ticket_status_changed" {
			notifCount++
		}
	}
	if notifCount != 1 {
		t.Fatalf("关闭自动化规则后不应继续发通知，当前通知数=%d", notifCount)
	}
}
