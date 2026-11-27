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
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestRecallMessage 验证撤回：时间线项保持原样，撤回作为独立记录追加；读取时正文、附件和译文都不再返回；
// 只有作者能撤回；内部聊天限 24 小时，内部备注不限；发给客户的消息要等更正流程；撤回后不能再编辑，重复撤回幂等。
func TestRecallMessage(t *testing.T) {
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
	subOwner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	subAlice := identity.Subject{Type: member.Type, ID: alice.ID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}

	// 1. 内部聊天：撤回自己的消息后，其他参与者只看到撤回状态
	chat, err := conversation.NewCreateDirectChat(env.DB, down).Handle(ctx, subOwner, conversation.CreateDirectChatParams{ParticipantID: alice.ID})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, subAlice, chat.ID, conversation.SendChatMessageBody{Text: "初稿"})
	if err != nil {
		t.Fatal(err)
	}
	recall := conversation.NewRecallMessage(env.DB, down)
	if err := recall.Handle(ctx, subAlice, chat.ID, msg.ID); err != nil {
		t.Fatal(err)
	}
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, subOwner, chat.ID, 0, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("读取时间线：%+v %v", items, err)
	}
	if !items[0].Recalled || items[0].Content.Text != "" || items[0].RecalledAt == nil {
		t.Fatalf("撤回后应只返回撤回状态：%+v", items[0])
	}
	if docs, err := conversation.LoadSearchDocs(ctx, env.DB, msg.ID); err != nil || len(docs) != 0 {
		t.Fatalf("撤回后应从全文索引移除：%+v %v", docs, err)
	}

	// 2. 不是作者不能撤回；撤回后不能再编辑；重复撤回幂等
	if err := recall.Handle(ctx, subOwner, chat.ID, msg.ID); !errors.Is(err, conversation.ErrMessageNotAuthor) {
		t.Fatalf("其他参与者不能撤回：%v", err)
	}
	if _, err := conversation.NewEditMessage(env.DB, down).Handle(ctx, subAlice, chat.ID, msg.ID, "改一下"); !errors.Is(err, conversation.ErrMessageNotEditable) {
		t.Fatalf("撤回后不能再编辑：%v", err)
	}
	if err := recall.Handle(ctx, subAlice, chat.ID, msg.ID); err != nil {
		t.Fatalf("重复撤回应幂等：%v", err)
	}

	// 3. 超过 24 小时不能再撤回
	old, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, subAlice, chat.ID, conversation.SendChatMessageBody{Text: "旧消息"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE timeline_items SET created_at = ? WHERE id = ?", time.Now().UTC().Add(-25*time.Hour), old.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := recall.Handle(ctx, subAlice, chat.ID, old.ID); !errors.Is(err, conversation.ErrMessageRecallWindow) {
		t.Fatalf("超过时间窗应被拒绝：%v", err)
	}

	// 4. 服务型会话：内部备注可以随时撤回；客户消息和发给客户的消息不能撤回
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "rahul", FromName: "Rahul", Text: "Where is my order?",
	})
	if err != nil {
		t.Fatal(err)
	}
	note, err := conversation.NewAddNote(env.DB, down).Handle(ctx, subAlice, rec.ConversationID, "初稿", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := recall.Handle(ctx, subAlice, rec.ConversationID, note.ID); err != nil {
		t.Fatalf("内部备注应可以撤回：%v", err)
	}
	if err := recall.Handle(ctx, subAlice, rec.ConversationID, rec.Item.ID); !errors.Is(err, conversation.ErrMessageNotAuthor) {
		t.Fatalf("客户的消息不能撤回：%v", err)
	}
	reply, err := conversation.NewReply(env.DB, down).Handle(ctx, subAlice, rec.ConversationID, conversation.ReplyBody{Text: "已经发货了"})
	if err != nil {
		t.Fatal(err)
	}
	if err := recall.Handle(ctx, subAlice, rec.ConversationID, reply.ID); !errors.Is(err, conversation.ErrMessageNotEditable) {
		t.Fatalf("发给客户的消息不能撤回：%v", err)
	}
}

func TestRecallPreviewLeakPreventionAndChatExternalNotes(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	down := conversation.Downstream{Roles: env.Roles}

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "recall-prev", Slug: "recall-prev", OwnerEmail: "owner@recallp.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, sys, member.AddParams{
		Email: "alice@recallp.com", Name: "Alice", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	aliceID := alice.ID
	subOwner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	subAlice := identity.Subject{Type: member.Type, ID: alice.ID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}

	recall := conversation.NewRecallMessage(env.DB, down)

	// 1. 内部聊天最后一条消息撤回后，列表 PreviewRecalled=true 且不泄露原文
	chat, err := conversation.NewCreateDirectChat(env.DB, down).Handle(ctx, subOwner, conversation.CreateDirectChatParams{ParticipantID: alice.ID})
	if err != nil {
		t.Fatal(err)
	}
	secretMsg, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, subAlice, chat.ID, conversation.SendChatMessageBody{Text: "不能外泄的敏感消息"})
	if err != nil {
		t.Fatal(err)
	}
	if err := recall.Handle(ctx, subAlice, chat.ID, secretMsg.ID); err != nil {
		t.Fatal(err)
	}
	chats, err := conversation.NewListChats(env.DB).Handle(ctx, subOwner, &conversation.ListChatsInput{})
	if err != nil || len(chats.Items) != 1 {
		t.Fatal(err)
	}
	if !chats.Items[0].PreviewRecalled || chats.Items[0].Preview != "" || chats.Items[0].PreviewAttachment != nil || chats.Items[0].PreviewUntranslated {
		t.Fatalf("撤回最后一条消息后聊天列表不应泄露任何预览信息：%+v", chats.Items[0])
	}

	// 2. 外部商务聊天（ChatExternal）中的内部备注允许作者撤回，对外消息不能撤回
	bizCh := &channel.Channel{
		ID: "019a0000-0000-7000-8000-000000000202", TenantID: tn.ID, Kind: channel.BusinessLink,
		Name: "Alice 商务专线", Settings: map[string]any{}, OwnerMemberID: &aliceID, Enabled: true,
	}
	if _, err := env.DB.NewInsert().Model(bizCh).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	reg := channel.DefaultRegistry()
	reg[channel.BusinessLink] = channel.TestConnector{}
	recBiz, err := conversation.NewReceive(env.DB, reg, down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: bizCh.ID, From: "visitor:p-rec", FromName: "Partner", Text: "Hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	bizMsg, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, subAlice, recBiz.ConversationID, conversation.SendChatMessageBody{
		Text: "Public reply",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recall.Handle(ctx, subAlice, recBiz.ConversationID, bizMsg.ID); !errors.Is(err, conversation.ErrMessageNotEditable) {
		t.Fatalf("外部商务聊天发给客户的消息不能撤回：%v", err)
	}
	bizNote, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, subAlice, recBiz.ConversationID, conversation.SendChatMessageBody{
		Text: "内部核对意见",
		Note: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recall.Handle(ctx, subAlice, recBiz.ConversationID, bizNote.ID); err != nil {
		t.Fatalf("外部商务聊天中的内部备注应允许作者撤回：%v", err)
	}
}
