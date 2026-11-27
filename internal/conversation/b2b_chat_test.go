package conversation_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tag"
	"agents-desk/internal/tenant"
)

// b2bRecordingConnector 记录发往外部商务渠道的每一条出站消息，用于测试外部单聊与外部群聊的投递路由。
type b2bRecordingConnector struct {
	sent []channel.Outgoing
}

func (r *b2bRecordingConnector) Capabilities() channel.Capabilities {
	return channel.Capabilities{MessageTypes: []string{"text", "image", "file"}, SupportsDirect: true, SupportsGroup: true}
}

func (r *b2bRecordingConnector) Send(_ context.Context, m channel.Outgoing) (string, bool, error) {
	r.sent = append(r.sent, m)
	return "ext-" + m.ItemID, true, nil
}

func TestExternalBusinessChatLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)
	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "alpha", OwnerEmail: "owner@alpha.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Beta", Slug: "beta", OwnerEmail: "owner@beta.test", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sysAlpha := identity.Subject{Type: identity.System, ID: "sync", Tenant: alpha.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	alice, err := addMember.Handle(ctx, sysAlpha, member.AddParams{Email: "alice@alpha.test", Name: "Alice", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := addMember.Handle(ctx, sysAlpha, member.AddParams{Email: "bob@alpha.test", Name: "Bob", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}

	subAlice := identity.Subject{Type: member.Type, ID: alice.ID, Tenant: alpha.ID, Permissions: auth.WithBase(nil)}
	subBob := identity.Subject{Type: member.Type, ID: bob.ID, Tenant: alpha.ID, Permissions: auth.WithBase(nil)}
	subBeta := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}

	// 为 Alice 创建两个个人商务渠道：网页商务沟通链接 + Telegram 个人号（模拟连接器）
	aliceID := alice.ID
	bizLink := &channel.Channel{
		ID: "019a0000-0000-7000-8000-000000000001", TenantID: alpha.ID, Kind: channel.BusinessLink,
		Name: "Alice 商务专线", Settings: map[string]any{}, OwnerMemberID: &aliceID, Enabled: true,
	}
	tgAcct := &channel.Channel{
		ID: "019a0000-0000-7000-8000-000000000002", TenantID: alpha.ID, Kind: channel.TelegramAccount,
		Name: "Alice TG (@alice_bd)", Settings: map[string]any{}, OwnerMemberID: &aliceID, Enabled: true,
	}
	for _, ch := range []*channel.Channel{bizLink, tgAcct} {
		if _, err := env.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	linkConn := &b2bRecordingConnector{}
	tgConn := &b2bRecordingConnector{}
	reg := channel.DefaultRegistry()
	reg[channel.BusinessLink] = linkConn
	reg[channel.TelegramAccount] = tgConn

	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	receive := conversation.NewReceive(env.DB, reg, down)
	deliver := conversation.NewDeliver(env.DB, reg, down)
	sendMsg := conversation.NewSendChatMessage(env.DB, down)
	listChats := conversation.NewListChats(env.DB)
	getChat := conversation.NewGetChat(env.DB)
	getUnread := conversation.NewGetChatUnread(env.DB)

	// 1. 网页商务沟通链接：外部商户发来消息 -> 归入 Alice 的外部商务单聊，不进入客服收件箱，Bob 未被拉入不可见
	rec1, err := receive.Handle(ctx, sysAlpha, conversation.ReceiveParams{
		ChannelID:  bizLink.ID,
		From:       "visitor:partner-1",
		FromName:   "Rahul Merchant",
		ExternalID: "msg-1",
		Text:       "Hello Alice, interested in payment API pricing",
	})
	if err != nil || !rec1.Created {
		t.Fatalf("商务链接首条消息写入失败：%v %+v", err, rec1)
	}
	inbox, err := conversation.NewGetInbox(env.DB).Handle(ctx, subAlice)
	if err != nil || inbox.All.Count != 0 {
		t.Fatalf("商务沟通消息不应进入客服收件箱：%v %+v", err, inbox)
	}
	if _, err := getChat.Handle(ctx, subBob, rec1.ConversationID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("未加入的同事不应看到他人的商务聊天：%v", err)
	}
	if _, err := getChat.Handle(ctx, subBeta, rec1.ConversationID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("其他工作区不应看到商务聊天：%v", err)
	}

	chat1, err := getChat.Handle(ctx, subAlice, rec1.ConversationID)
	if err != nil || chat1.Scope != conversation.ChatExternal || chat1.Mode != conversation.ChatDirect ||
		chat1.Channel == nil || chat1.Channel.ID != bizLink.ID || chat1.Contact == nil || chat1.Peer == nil || chat1.Peer.Name != "Rahul Merchant" {
		t.Fatalf("商务单聊详情异常：%v %+v", err, chat1)
	}

	// 2. 商务聊天回复支持对外翻译发送并走 Deliver 投递，内部备注仅成员可见且不投递
	replyItem, err := sendMsg.Handle(ctx, subAlice, chat1.ID, conversation.SendChatMessageBody{
		Text:        "你好，报价单稍后发给你",
		Translation: &conversation.Translation{Language: "en", Text: "Hi, I will send the pricing sheet shortly"},
	})
	if err != nil || replyItem.Kind != conversation.Message || replyItem.Visibility != conversation.Everyone ||
		replyItem.Content.Text != "Hi, I will send the pricing sheet shortly" || replyItem.Content.Original != "你好，报价单稍后发给你" ||
		replyItem.Delivery == nil || *replyItem.Delivery != conversation.Sending {
		t.Fatalf("外部商务回复结果异常：%v %+v", err, replyItem)
	}
	if err := deliver.Job(ctx, conversation.Appended{TenantID: alpha.ID, ConversationID: chat1.ID, ItemID: replyItem.ID, Seq: replyItem.Seq}); err != nil {
		t.Fatalf("投递商务回复失败：%v", err)
	}
	if len(linkConn.sent) != 1 || linkConn.sent[0].Text != "Hi, I will send the pricing sheet shortly" {
		t.Fatalf("商务链接连接器未收到预期译文：%+v", linkConn.sent)
	}

	// 拉同事 Bob 进外部 1:1 商务单聊协作并写内部备注，备注不触发外部投递
	chat1, err = conversation.NewAddChatParticipants(env.DB, down).Handle(ctx, subAlice, chat1.ID, []string{bob.ID})
	if err != nil || chat1.ParticipantCount != 3 {
		t.Fatalf("邀请同事协作商务单聊失败：%v %+v", err, chat1)
	}
	noteItem, err := sendMsg.Handle(ctx, subAlice, chat1.ID, conversation.SendChatMessageBody{
		Text:     "@Bob 这个客户日流水 50 万美金，帮忙给个阶梯报价",
		Mentions: []string{bob.ID},
		Note:     true,
	})
	if err != nil || noteItem.Kind != conversation.Note || noteItem.Visibility != conversation.MembersOnly || noteItem.Delivery != nil {
		t.Fatalf("外部商务内部备注异常：%v %+v", err, noteItem)
	}
	if err := deliver.Job(ctx, conversation.Appended{TenantID: alpha.ID, ConversationID: chat1.ID, ItemID: noteItem.ID, Seq: noteItem.Seq}); err != nil {
		t.Fatal(err)
	}
	if len(linkConn.sent) != 1 {
		t.Fatalf("内部备注不应发往外部渠道：%+v", linkConn.sent)
	}

	// 渠道归属人与外部联系人不可被移出，但协作同事可以退出
	if _, err := conversation.NewRemoveChatParticipant(env.DB, down).Handle(ctx, subBob, chat1.ID, conversation.MemberActor, alice.ID); !errors.Is(err, conversation.ErrChatOwnerProtected) {
		t.Fatalf("渠道归属人不应被移出：%v", err)
	}
	if _, err := conversation.NewRemoveChatParticipant(env.DB, down).Handle(ctx, subAlice, chat1.ID, conversation.ContactActor, chat1.Contact.ID); !errors.Is(err, conversation.ErrChatContactProtected) {
		t.Fatalf("外部联系人不应被移出：%v", err)
	}
	if err := conversation.NewLeaveChat(env.DB, down).Handle(ctx, subBob, chat1.ID); err != nil {
		t.Fatalf("协作同事退出外部商务聊失败：%v", err)
	}

	// 3. Telegram 个人号外部群聊：多发言人 + 归属成员从官方 App 发出同步消息 + 平台回复投递到 external_chat_id
	past := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	grpRec1, err := receive.Handle(ctx, sysAlpha, conversation.ReceiveParams{
		ChannelID:      tgAcct.ID,
		From:           "tg:user:1001",
		FromName:       "Vikram (Merchant)",
		ExternalID:     "tg:-100888:1",
		ExternalChatID: "tg:channel:100888:999999",
		IsGroup:        true,
		ChatTitle:      "India Payout Integration",
		CreatedAt:      &past,
		Text:           "Can we enable UPI callback?",
	})
	if err != nil || !grpRec1.Created || grpRec1.Item.Actor.Name != "Vikram (Merchant)" {
		t.Fatalf("Telegram 外部群首条消息失败：%v %+v", err, grpRec1)
	}
	grpRec2, err := receive.Handle(ctx, sysAlpha, conversation.ReceiveParams{
		ChannelID:      tgAcct.ID,
		From:           "tg:user:1002",
		FromName:       "Priya (Tech)",
		ExternalID:     "tg:-100888:2",
		ExternalChatID: "tg:channel:100888:999999",
		IsGroup:        true,
		ChatTitle:      "India Payout Integration v2",
		Text:           "Webhook URL is ready",
	})
	if err != nil || grpRec2.Created || grpRec2.ConversationID != grpRec1.ConversationID || grpRec2.Item.Actor.Name != "Priya (Tech)" {
		t.Fatalf("Telegram 外部群后续消息未归入同一群聊：%v %+v", err, grpRec2)
	}
	// 归属成员自己在官方 Telegram App 中发出的消息同步入库（FromOwner=true）
	ownerSync, err := receive.Handle(ctx, sysAlpha, conversation.ReceiveParams{
		ChannelID:      tgAcct.ID,
		ExternalID:     "tg:-100888:3",
		ExternalChatID: "tg:channel:100888:999999",
		IsGroup:        true,
		FromOwner:      true,
		Text:           "Sure, enabling now!",
	})
	if err != nil || ownerSync.ConversationID != grpRec1.ConversationID || ownerSync.Item.Actor.Type != conversation.MemberActor || ownerSync.Item.Actor.ID != alice.ID {
		t.Fatalf("归属成员同步消息异常：%v %+v", err, ownerSync)
	}

	grpChat, err := getChat.Handle(ctx, subAlice, grpRec1.ConversationID)
	if err != nil || grpChat.Scope != conversation.ChatExternal || grpChat.Mode != conversation.ChatGroup ||
		grpChat.Name != "India Payout Integration v2" || grpChat.ExternalChatID != "tg:channel:100888:999999" {
		t.Fatalf("Telegram 外部群详情异常：%v %+v", err, grpChat)
	}

	// 从平台回复该 Telegram 外部群，Deliver 应将 external_chat_id 传给连接器的 ExternalTo
	grpReply, err := sendMsg.Handle(ctx, subAlice, grpChat.ID, conversation.SendChatMessageBody{Text: "Done, please test callback."})
	if err != nil {
		t.Fatalf("回复 Telegram 外部群失败：%v", err)
	}
	if err := deliver.Job(ctx, conversation.Appended{TenantID: alpha.ID, ConversationID: grpChat.ID, ItemID: grpReply.ID, Seq: grpReply.Seq}); err != nil {
		t.Fatalf("投递 Telegram 外部群消息失败：%v", err)
	}
	if len(tgConn.sent) != 1 || tgConn.sent[0].ExternalTo != "tg:channel:100888:999999" || tgConn.sent[0].Text != "Done, please test callback." {
		t.Fatalf("Telegram 外部群投递参数异常：%+v", tgConn.sent)
	}

	// 4. 列表按 scope / channel_id / 联系人名筛选，以及未读分栏统计
	internalDM, err := conversation.NewCreateDirectChat(env.DB, down).Handle(ctx, subAlice, conversation.CreateDirectChatParams{ParticipantID: bob.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sendMsg.Handle(ctx, subBob, internalDM.ID, conversation.SendChatMessageBody{Text: "内部同步一下"}); err != nil {
		t.Fatal(err)
	}

	extList, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{
		CursorParams: page.CursorParams{PerPage: 20},
		Scope:        conversation.ChatExternal,
	})
	if err != nil || len(extList.Items) != 2 {
		t.Fatalf("按 scope=external 筛选应返回 2 条商务聊天：%v %+v", err, extList)
	}
	tgOnly, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{
		CursorParams: page.CursorParams{PerPage: 20},
		Scope:        conversation.ChatExternal,
		ChannelID:    tgAcct.ID,
	})
	if err != nil || len(tgOnly.Items) != 1 || tgOnly.Items[0].ID != grpChat.ID {
		t.Fatalf("按 channel_id 筛选应返回 1 条 Telegram 群聊：%v %+v", err, tgOnly)
	}
	byContact, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{
		CursorParams: page.CursorParams{PerPage: 20},
		Q:            "Rahul",
	})
	if err != nil || len(byContact.Items) != 1 || byContact.Items[0].ID != chat1.ID {
		t.Fatalf("按外部联系人名搜索应命中商务单聊：%v %+v", err, byContact)
	}

	// 外部再发一条新消息后核对内部/外部未读拆分
	if _, err := receive.Handle(ctx, sysAlpha, conversation.ReceiveParams{
		ChannelID:      tgAcct.ID,
		From:           "tg:user:1001",
		FromName:       "Vikram (Merchant)",
		ExternalID:     "tg:-100888:4",
		ExternalChatID: "tg:channel:100888:999999",
		IsGroup:        true,
		Text:           "Callback received!",
	}); err != nil {
		t.Fatal(err)
	}
	unread, err := getUnread.Handle(ctx, subAlice)
	if err != nil || unread.InternalUnreadMessages != 1 || unread.ExternalUnreadMessages != 1 || unread.UnreadMessages != 2 {
		t.Fatalf("内部/外部聊天未读分组统计异常：%v %+v", err, unread)
	}
}

func TestBusinessPolicyEnforcementOnInbound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)
	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "alpha", OwnerEmail: "owner@alpha.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sysAlpha := identity.Subject{Type: identity.System, ID: "sync", Tenant: alpha.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	alice, err := addMember.Handle(ctx, sysAlpha, member.AddParams{Email: "alice@alpha.test", Name: "Alice", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := addMember.Handle(ctx, sysAlpha, member.AddParams{Email: "bob@alpha.test", Name: "Bob", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	vipTag, err := tag.NewCreate(env.DB).Handle(ctx, sysAlpha, tag.Changes{Name: "VIP Partner", Color: tag.Yellow})
	if err != nil {
		t.Fatal(err)
	}

	// 在个人商务链接中配置默认协作成员（含有效同事 Bob 与一个已不存在的成员 ID）与默认标签（含有效标签与已删除标签 ID）
	aliceID := alice.ID
	settings := map[string]any{}
	channel.SetBusinessPolicy(settings, channel.BusinessPolicy{
		DefaultCollaboratorIDs: []string{bob.ID, "019a0000-0000-7000-8000-00000000dead"},
		DefaultTagIDs:          []string{vipTag.ID, "019a0000-0000-7000-8000-00000000beef"},
	})
	bizLink := &channel.Channel{
		ID: "019a0000-0000-7000-8000-000000000099", TenantID: alpha.ID, Kind: channel.BusinessLink,
		Name: "VIP 合作专线", Settings: settings, OwnerMemberID: &aliceID, Enabled: true,
	}
	if _, err := env.DB.NewInsert().Model(bizLink).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	reg := channel.DefaultRegistry()
	reg[channel.BusinessLink] = &b2bRecordingConnector{}
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	rec, err := conversation.NewReceive(env.DB, reg, down).Handle(ctx, sysAlpha, conversation.ReceiveParams{
		ChannelID:  bizLink.ID,
		From:       "visitor:partner-policy-1",
		FromName:   "Carlos Partner",
		ExternalID: "msg-policy-1",
		Text:       "Hi Alice, let's discuss LATAM distribution",
	})
	if err != nil || !rec.Created {
		t.Fatalf("入站消息应成功创建商务对话且忽略失效的协作成员与标签：%v %+v", err, rec)
	}

	// Bob 已被自动拉入协作，可以直接读取该商务聊天；联系人已被自动打上 VIP Partner 标签
	subBob := identity.Subject{Type: member.Type, ID: bob.ID, Tenant: alpha.ID, Permissions: auth.WithBase(nil)}
	got, err := conversation.NewGetChat(env.DB).Handle(ctx, subBob, rec.ConversationID)
	if err != nil || got.ParticipantCount != 3 || got.Contact == nil {
		t.Fatalf("默认协作成员 Bob 应自动加入商务聊天：err=%v got=%+v", err, got)
	}
	var tagged bool
	if err := env.DB.NewRaw(`SELECT EXISTS (SELECT 1 FROM contact_tags WHERE tenant_id = ? AND contact_id = ? AND tag_id = ?)`,
		alpha.ID, got.Contact.ID, vipTag.ID).Scan(ctx, &tagged); err != nil || !tagged {
		t.Fatalf("新联系人应自动打上渠道默认标签：tagged=%v err=%v", tagged, err)
	}
}
