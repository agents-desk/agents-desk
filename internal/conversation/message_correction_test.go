package conversation_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestMessageCorrection 验证消息更正：
// 更正作为新消息发出并指向被更正的原消息，原消息在成员和客户时间线读取时投影成已更正；
// 投递给外部渠道时自动引用原消息外部 ID；多次更正只保留最近一次；不支持客户消息、备注、事件和已撤回消息。
func TestMessageCorrection(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	down := conversation.Downstream{}

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	add := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	alice, err := add.Handle(ctx, sys, member.AddParams{Email: "alice@alpha.com", Name: "Alice", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := add.Handle(ctx, sys, member.AddParams{Email: "bob@alpha.com", Name: "Bob", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	subOwner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	subAlice := identity.Subject{Type: member.Type, ID: alice.ID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}
	subBob := identity.Subject{Type: member.Type, ID: bob.ID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}

	// 1. 服务型会话：成员回复后发送更正，原消息保持不可变，成员与客户读取均投影更正关系
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "rahul", FromName: "Rahul", Text: "When does my order arrive?",
	})
	if err != nil {
		t.Fatal(err)
	}
	replyAct := conversation.NewReply(env.DB, down)
	orig, err := replyAct.Handle(ctx, subAlice, rec.ConversationID, conversation.ReplyBody{Text: "周二送达"})
	if err != nil {
		t.Fatal(err)
	}

	// 模拟原消息已送达渠道并拿到外部消息 ID，再发出更正并运行投递任务
	conn := &recordingConnector{}
	reg := channel.Registry{channel.Test: conn}
	deliver := conversation.NewDeliver(env.DB, reg, down)
	if err := deliver.Job(ctx, conversation.Appended{TenantID: tn.ID, ConversationID: rec.ConversationID, ItemID: orig.ID, Seq: orig.Seq}); err != nil {
		t.Fatal(err)
	}

	// 同事 Bob 也可以更正发给客户的回复
	corr1, err := replyAct.Handle(ctx, subBob, rec.ConversationID, conversation.ReplyBody{
		Text:           "更正：实际周三送达",
		CorrectsItemID: orig.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if corr1.CorrectsItemID == nil || *corr1.CorrectsItemID != orig.ID {
		t.Fatalf("更正消息应标出原消息 ID：%+v", corr1)
	}
	if corr1.Delivery == nil || *corr1.Delivery != conversation.Sending {
		t.Fatalf("更正消息应进入发往客户的投递队列：%+v", corr1.Delivery)
	}
	if err := deliver.Job(ctx, conversation.Appended{TenantID: tn.ID, ConversationID: rec.ConversationID, ItemID: corr1.ID, Seq: corr1.Seq}); err != nil {
		t.Fatal(err)
	}
	if len(conn.sent) != 2 || conn.sent[1].ReplyToExternalID != "rec-"+orig.ID {
		t.Fatalf("更正消息投递应自动引用原消息外部 ID：%+v", conn.sent)
	}

	// 成员视角读取时间线：原消息正文不变，但投影出 corrected_by 和 corrected_at
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, subAlice, rec.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]conversation.ItemDTO{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if got := byID[orig.ID]; got.Content.Text != "周二送达" || got.CorrectedBy == nil || *got.CorrectedBy != corr1.ID || got.CorrectedAt == nil {
		t.Fatalf("原消息应保留原文并投影为已更正：%+v", got)
	}
	if got := byID[corr1.ID]; got.CorrectsItemID == nil || *got.CorrectsItemID != orig.ID {
		t.Fatalf("更正消息应投影 corrects_item_id：%+v", got)
	}

	// 客户视角读取连续时间线：同样投影 corrected_by / corrects_item_id
	var contactID string
	if err := env.DB.NewSelect().Table("conversations").Column("contact_id").Where("id = ?", rec.ConversationID).Scan(ctx, &contactID); err != nil {
		t.Fatal(err)
	}
	subContact := identity.Subject{Type: contact.Type, ID: contactID, Tenant: tn.ID}
	stream, err := conversation.NewContactStream(env.DB).Handle(ctx, subContact, ch.ID, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	cByID := map[string]conversation.ContactItemDTO{}
	for _, it := range stream.Items {
		cByID[it.ID] = it
	}
	if got := cByID[orig.ID]; got.CorrectedBy == nil || *got.CorrectedBy != corr1.ID || got.CorrectedAt == nil {
		t.Fatalf("客户时间线中的原消息应投影为已更正：%+v", got)
	}
	if got := cByID[corr1.ID]; got.CorrectsItemID == nil || *got.CorrectsItemID != orig.ID {
		t.Fatalf("客户时间线中的更正消息应投影 corrects_item_id：%+v", got)
	}

	// 2. 同一条原消息再次更正时，原消息只保留最近一次更正
	corr2, err := replyAct.Handle(ctx, subAlice, rec.ConversationID, conversation.ReplyBody{
		Text:           "更正：周四上午送达",
		CorrectsItemID: orig.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err = conversation.NewTimeline(env.DB).Handle(ctx, subAlice, rec.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	byID = map[string]conversation.ItemDTO{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if got := byID[orig.ID]; got.CorrectedBy == nil || *got.CorrectedBy != corr2.ID {
		t.Fatalf("再次更正后原消息应指向最新更正：%+v", got)
	}
	if got := byID[corr1.ID]; got.CorrectsItemID != nil {
		t.Fatalf("被覆盖的旧更正记录不应再绑定到原消息：%+v", got)
	}
	if got := byID[corr2.ID]; got.CorrectsItemID == nil || *got.CorrectsItemID != orig.ID {
		t.Fatalf("最新更正消息应指向原消息：%+v", got)
	}

	// 3. 客户消息、内部备注、系统事件不能被更正；其他工作区或会话的消息返回 ErrNotFound
	if _, err := replyAct.Handle(ctx, subAlice, rec.ConversationID, conversation.ReplyBody{
		Text: "改客户的话", CorrectsItemID: rec.Item.ID,
	}); !errors.Is(err, conversation.ErrMessageNotCorrectable) {
		t.Fatalf("客户消息不应允许更正：%v", err)
	}
	note, err := conversation.NewAddNote(env.DB, down).Handle(ctx, subAlice, rec.ConversationID, conversation.NoteBody{Text: "内部核对中"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replyAct.Handle(ctx, subAlice, rec.ConversationID, conversation.ReplyBody{
		Text: "更正备注", CorrectsItemID: note.ID,
	}); !errors.Is(err, conversation.ErrMessageNotCorrectable) {
		t.Fatalf("内部备注不应允许更正：%v", err)
	}
	for _, it := range items {
		if it.Kind == conversation.Event {
			if _, err := replyAct.Handle(ctx, subAlice, rec.ConversationID, conversation.ReplyBody{
				Text: "更正事件", CorrectsItemID: it.ID,
			}); !errors.Is(err, conversation.ErrMessageNotCorrectable) {
				t.Fatalf("系统事件不应允许更正：%v", err)
			}
			break
		}
	}

	// 4. 内部聊天：超过编辑时间窗后作者仍可发送更正；非作者不能更正；已撤回消息不能更正
	chat, err := conversation.NewCreateDirectChat(env.DB, down).Handle(ctx, subOwner, conversation.CreateDirectChatParams{ParticipantID: alice.ID})
	if err != nil {
		t.Fatal(err)
	}
	sendChat := conversation.NewSendChatMessage(env.DB, down)
	chatMsg, err := sendChat.Handle(ctx, subAlice, chat.ID, conversation.SendChatMessageBody{Text: "下周二开会"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE timeline_items SET created_at = ? WHERE id = ?", time.Now().UTC().Add(-time.Hour), chatMsg.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := sendChat.Handle(ctx, subOwner, chat.ID, conversation.SendChatMessageBody{
		Text: "替别人更正", CorrectsItemID: chatMsg.ID,
	}); !errors.Is(err, conversation.ErrMessageNotAuthor) {
		t.Fatalf("内部聊天非作者不应允许更正：%v", err)
	}
	chatCorr, err := sendChat.Handle(ctx, subAlice, chat.ID, conversation.SendChatMessageBody{
		Text: "更正：下周三开会", CorrectsItemID: chatMsg.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if chatCorr.CorrectsItemID == nil || *chatCorr.CorrectsItemID != chatMsg.ID {
		t.Fatalf("内部聊天更正消息应投影 corrects_item_id：%+v", chatCorr)
	}
	// 跨会话引用返回 ErrNotFound
	if _, err := replyAct.Handle(ctx, subAlice, rec.ConversationID, conversation.ReplyBody{
		Text: "跨会话更正", CorrectsItemID: chatMsg.ID,
	}); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("跨会话更正应返回 ErrNotFound：%v", err)
	}

	// 已撤回的消息不能再更正
	recalledMsg, err := sendChat.Handle(ctx, subAlice, chat.ID, conversation.SendChatMessageBody{Text: "撤回前"})
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.NewRecallMessage(env.DB, down).Handle(ctx, subAlice, chat.ID, recalledMsg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sendChat.Handle(ctx, subAlice, chat.ID, conversation.SendChatMessageBody{
		Text: "更正已撤回消息", CorrectsItemID: recalledMsg.ID,
	}); !errors.Is(err, conversation.ErrMessageNotCorrectable) {
		t.Fatalf("已撤回的消息不应允许更正：%v", err)
	}

	// 5. 外部商务聊天：对外回复可更正，内部备注不能作为更正发出
	aliceID := alice.ID
	bizCh := &channel.Channel{
		ID: "019a0000-0000-7000-8000-000000000099", TenantID: tn.ID, Kind: channel.BusinessLink,
		Name: "商务沟通", Audience: channel.AudiencePartner, Settings: map[string]any{}, OwnerMemberID: &aliceID, Enabled: true,
	}
	if _, err := env.DB.NewInsert().Model(bizCh).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	bizReg := channel.DefaultRegistry()
	bizReg[channel.BusinessLink] = &recordingConnector{}
	bizRec, err := conversation.NewReceive(env.DB, bizReg, down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: bizCh.ID, From: "partner-1", FromName: "Partner", Text: "报价单是多少？",
	})
	if err != nil {
		t.Fatal(err)
	}
	bizReply, err := sendChat.Handle(ctx, subAlice, bizRec.ConversationID, conversation.SendChatMessageBody{Text: "报价 1000 元"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sendChat.Handle(ctx, subAlice, bizRec.ConversationID, conversation.SendChatMessageBody{
		Text: "内部备注不能当更正", Note: true, CorrectsItemID: bizReply.ID,
	}); !errors.Is(err, conversation.ErrMessageNotCorrectable) {
		t.Fatalf("内部备注不应允许作为更正：%v", err)
	}
	bizCorr, err := sendChat.Handle(ctx, subAlice, bizRec.ConversationID, conversation.SendChatMessageBody{
		Text: "更正：报价 1200 元", CorrectsItemID: bizReply.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bizCorr.CorrectsItemID == nil || *bizCorr.CorrectsItemID != bizReply.ID {
		t.Fatalf("外部商务聊天更正应投影 corrects_item_id：%+v", bizCorr)
	}
	if bizCorr.Delivery == nil || *bizCorr.Delivery != conversation.Sending {
		t.Fatalf("外部商务聊天更正应写入投递记录：%+v", bizCorr.Delivery)
	}

	// 6. 其他工作区成员无法更正本工作区的消息
	otherTn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "Beta", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	subOther := identity.Subject{Type: member.Type, ID: otherTn.OwnerID, Tenant: otherTn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	if _, err := replyAct.Handle(ctx, subOther, rec.ConversationID, conversation.ReplyBody{
		Text: "跨工作区", CorrectsItemID: orig.ID,
	}); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("其他工作区成员不应访问本工作区会话：%v", err)
	}
}
