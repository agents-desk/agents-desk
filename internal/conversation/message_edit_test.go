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

// TestEditMessage 验证消息编辑：编辑作为新的修订追加，时间线项本身不变；只有作者能编辑；
// 内部备注不受时间限制，内部聊天的消息限 15 分钟；发给客户的消息要等更正流程，不能直接改。
func TestEditMessage(t *testing.T) {
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
	edit := conversation.NewEditMessage(env.DB, down)

	// 1. 内部备注可以编辑，没有时间窗；编辑后返回最新正文和编辑次数
	note, err := conversation.NewAddNote(env.DB, down).Handle(ctx, subAlice, rec.ConversationID, "初稿", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE timeline_items SET created_at = ? WHERE id = ?", time.Now().UTC().Add(-time.Hour), note.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	edited, err := edit.Handle(ctx, subAlice, rec.ConversationID, note.ID, "改过的备注")
	if err != nil {
		t.Fatal(err)
	}
	if edited.Content.Text != "改过的备注" || edited.Revision != 1 || edited.EditedAt == nil {
		t.Fatalf("编辑后的备注：%+v", edited)
	}
	var stored conversation.Item
	if err := env.DB.NewSelect().Model(&stored).Where("id = ?", note.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.Content.Text != "初稿" {
		t.Fatalf("时间线项本身不应被改写：%+v", stored.Content)
	}

	// 2. 不是作者不能编辑
	if _, err := edit.Handle(ctx, subBob, rec.ConversationID, note.ID, "别人改的"); !errors.Is(err, conversation.ErrMessageNotAuthor) {
		t.Fatalf("非作者应被拒绝：%v", err)
	}
	// 3. 客户的消息不是成员发出的，不能编辑
	if _, err := edit.Handle(ctx, subAlice, rec.ConversationID, rec.Item.ID, "改客户的话"); !errors.Is(err, conversation.ErrMessageNotAuthor) {
		t.Fatalf("客户的消息应被拒绝：%v", err)
	}

	// 4. 发给客户的消息不能编辑，要等更正流程
	reply, err := conversation.NewReply(env.DB, down).Handle(ctx, subAlice, rec.ConversationID, conversation.ReplyBody{Text: "已经发货了"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edit.Handle(ctx, subAlice, rec.ConversationID, reply.ID, "改口"); !errors.Is(err, conversation.ErrMessageNotEditable) {
		t.Fatalf("发给客户的消息不能直接编辑：%v", err)
	}

	// 5. 内部聊天：自己的消息可以编辑，其他参与者不能编辑
	chat, err := conversation.NewCreateDirectChat(env.DB, down).Handle(ctx, subOwner, conversation.CreateDirectChatParams{ParticipantID: alice.ID})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, subAlice, chat.ID, conversation.SendChatMessageBody{Text: "初稿"})
	if err != nil {
		t.Fatal(err)
	}
	editedChat, err := edit.Handle(ctx, subAlice, chat.ID, msg.ID, "改过的消息")
	if err != nil || editedChat.Content.Text != "改过的消息" || editedChat.Revision != 1 {
		t.Fatalf("内部聊天消息编辑：%+v %v", editedChat, err)
	}
	if _, err := edit.Handle(ctx, subOwner, chat.ID, msg.ID, "别人改的"); !errors.Is(err, conversation.ErrMessageNotAuthor) {
		t.Fatalf("其他参与者不能编辑：%v", err)
	}

	// 6. 内部聊天的消息超过 15 分钟不能再编辑
	if _, err := env.DB.NewRaw("UPDATE timeline_items SET created_at = ? WHERE id = ?", time.Now().UTC().Add(-20*time.Minute), msg.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := edit.Handle(ctx, subAlice, chat.ID, msg.ID, "太晚了"); !errors.Is(err, conversation.ErrMessageEditWindow) {
		t.Fatalf("超过时间窗应被拒绝：%v", err)
	}
}
