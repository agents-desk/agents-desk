package conversation_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
	"agents-desk/internal/tenant"
)

// TestInternalChatLifecycleIsolationAndPush 验证内部聊天核心规则：
//   - 单聊（direct）双向发起幂等、禁止与自己单聊、预留 bot 类型边界；
//   - 群聊（group）建群、改名、加人、踢人、退群，单聊拒绝群操作；
//   - 严格参与者隔离：即使拥有 ScopeAll 的主管理员也看不到自己未参与的私聊和私群，且跨工作区隔离；
//   - 发消息、@提及、未读计数与 MarkRead、实时推送与提醒（仅推给参与者且不提醒发送者）；
//   - 服务型/工单专有动作按 kind 拒绝聊天，收件箱与工单列表不混入聊天；
//   - 统一翻译公理：聊天消息对其他语言同事标为 untranslated，对发送者本人不标为 untranslated。
func TestInternalChatLifecycleIsolationAndPush(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "B主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sysA := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	alice, err := addMember.Handle(ctx, sysA, member.AddParams{Email: "alice@alpha.com", Name: "Alice", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := addMember.Handle(ctx, sysA, member.AddParams{Email: "bob@alpha.com", Name: "Bob", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	// 设置不同成员偏好语言以验证统一翻译标记
	if _, err := env.DB.NewRaw("UPDATE members SET language = 'zh-CN' WHERE id = ?", alice.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE members SET language = 'en' WHERE id = ?", bob.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	subOwner := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	subAlice := identity.Subject{Type: member.Type, ID: alice.ID, Tenant: alpha.ID, Permissions: auth.WithBase(agentRole.Permissions)}
	subBob := identity.Subject{Type: member.Type, ID: bob.ID, Tenant: alpha.ID, Permissions: auth.WithBase(agentRole.Permissions)}
	subBeta := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}

	createDM := conversation.NewCreateDirectChat(env.DB, down)
	createGroup := conversation.NewCreateGroupChat(env.DB, down)
	listChats := conversation.NewListChats(env.DB)
	getChat := conversation.NewGetChat(env.DB)
	getUnread := conversation.NewGetChatUnread(env.DB)
	sendMsg := conversation.NewSendChatMessage(env.DB, down)

	// 1. 单聊边界与幂等性
	if _, err := createDM.Handle(ctx, subAlice, conversation.CreateDirectChatParams{ParticipantID: alice.ID}); !errors.Is(err, conversation.ErrChatSelfDirect) {
		t.Fatalf("自己单聊应报错：%v", err)
	}
	if _, err := createDM.Handle(ctx, subAlice, conversation.CreateDirectChatParams{ParticipantType: conversation.BotActor, ParticipantID: bob.ID}); !errors.Is(err, conversation.ErrChatUnsupportedParticipant) {
		t.Fatalf("未启用的 bot 单聊应报错：%v", err)
	}
	dm1, err := createDM.Handle(ctx, subAlice, conversation.CreateDirectChatParams{ParticipantID: bob.ID})
	if err != nil || dm1.Mode != conversation.ChatDirect || dm1.Peer == nil || dm1.Peer.ID != bob.ID || dm1.ParticipantCount != 2 {
		t.Fatalf("Alice 发起单聊失败：%v %+v", err, dm1)
	}
	dm2, err := createDM.Handle(ctx, subBob, conversation.CreateDirectChatParams{ParticipantID: alice.ID})
	if err != nil || dm2.ID != dm1.ID || dm2.Peer == nil || dm2.Peer.ID != alice.ID {
		t.Fatalf("Bob 反向打开单聊应幂等返回同一条：%v %+v", err, dm2)
	}

	// 单聊拒绝群管理操作
	if _, err := conversation.NewUpdateChat(env.DB, down).Handle(ctx, subAlice, dm1.ID, conversation.UpdateChatParams{Name: "非法改名"}); !errors.Is(err, conversation.ErrChatGroupOnly) {
		t.Fatalf("单聊改名应报 ErrChatGroupOnly：%v", err)
	}
	if err := conversation.NewLeaveChat(env.DB, down).Handle(ctx, subAlice, dm1.ID); !errors.Is(err, conversation.ErrChatGroupOnly) {
		t.Fatalf("单聊退群应报 ErrChatGroupOnly：%v", err)
	}

	// 2. 严格参与者隔离：主管理员（ScopeAll）与跨工作区管理员均不可见 Alice & Bob 的私聊
	if _, err := getChat.Handle(ctx, subOwner, dm1.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("ScopeAll 主管理员不应看到未参与的私聊：%v", err)
	}
	if _, err := getChat.Handle(ctx, subBeta, dm1.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("跨工作区不应看到私聊：%v", err)
	}
	ownerList, err := listChats.Handle(ctx, subOwner, &conversation.ListChatsInput{CursorParams: page.CursorParams{PerPage: 20}})
	if err != nil || len(ownerList.Items) != 0 {
		t.Fatalf("主管理员聊天列表不应包含他人私聊：%v count=%d", err, len(ownerList.Items))
	}

	// 3. 发送消息、实时推送（仅推给参与者）、未读统计、统一翻译标记与 MarkRead
	ownerSub := hub.Subscribe(push.Channel(alpha.ID, alpha.OwnerID))
	defer ownerSub.Close()
	bobSub := hub.Subscribe(push.Channel(alpha.ID, bob.ID))
	defer bobSub.Close()

	msg, err := sendMsg.Handle(ctx, subAlice, dm1.ID, conversation.SendChatMessageBody{Text: "印度通道恢复了吗？", Mentions: []string{bob.ID}})
	if err != nil || msg.Kind != conversation.Message {
		t.Fatalf("发送单聊消息失败：%v %+v", err, msg)
	}
	select {
	case raw := <-bobSub.C():
		var ev push.Event
		if err := json.Unmarshal(raw.Data, &ev); err != nil || ev.Type != push.Chat || ev.ID != dm1.ID || ev.Alert != push.AlertMention {
			t.Fatalf("Bob 应收到带 mention 提醒的 push.Chat：%v %+v", err, ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Bob 未收到实时推送")
	}
	select {
	case raw := <-ownerSub.C():
		t.Fatalf("非参与者主管理员不应收到私聊推送：%s", string(raw.Data))
	case <-time.After(100 * time.Millisecond):
	}

	// 未读统计：Bob 未读 1 条，Alice 自己发的未读 0 条
	bobUnread, err := getUnread.Handle(ctx, subBob)
	if err != nil || bobUnread.UnreadChats != 1 || bobUnread.UnreadMessages != 1 {
		t.Fatalf("Bob 未读统计异常：%v %+v", err, bobUnread)
	}
	aliceUnread, err := getUnread.Handle(ctx, subAlice)
	if err != nil || aliceUnread.UnreadChats != 0 || aliceUnread.UnreadMessages != 0 {
		t.Fatalf("Alice 自己发消息不应计未读：%v %+v", err, aliceUnread)
	}

	// 统一翻译公理：英文成员 Bob 读取时间线时该消息标为 untranslated，作者 Alice 自己读不标为 untranslated
	tl := conversation.NewTimeline(env.DB)
	bobItems, err := tl.Handle(ctx, subBob, dm1.ID, 0, 50)
	if err != nil || len(bobItems) != 1 || !bobItems[0].Untranslated {
		t.Fatalf("Bob 看到未翻译的跨语言聊天消息应标为 untranslated：%v %+v", err, bobItems)
	}
	aliceItems, err := tl.Handle(ctx, subAlice, dm1.ID, 0, 50)
	if err != nil || len(aliceItems) != 1 || aliceItems[0].Untranslated {
		t.Fatalf("作者 Alice 自己看到的消息不应标为 untranslated：%v %+v", err, aliceItems)
	}

	// 标记已读后未读清零
	if err := conversation.NewMarkRead(env.DB, hub).Handle(ctx, subBob, dm1.ID, msg.Seq); err != nil {
		t.Fatal(err)
	}
	bobUnread2, err := getUnread.Handle(ctx, subBob)
	if err != nil || bobUnread2.UnreadChats != 0 || bobUnread2.UnreadMessages != 0 {
		t.Fatalf("标记已读后未读应清零：%v %+v", err, bobUnread2)
	}

	// 4. 群聊生命周期：建群 -> 改名 -> 拉人 -> 踢人 -> 退群
	grp, err := createGroup.Handle(ctx, subAlice, conversation.CreateGroupChatParams{
		Name:      "中印协作群",
		MemberIDs: []string{bob.ID},
	})
	if err != nil || grp.Mode != conversation.ChatGroup || grp.Name != "中印协作群" || grp.ParticipantCount != 2 {
		t.Fatalf("建群失败：%v %+v", err, grp)
	}
	grp, err = conversation.NewUpdateChat(env.DB, down).Handle(ctx, subAlice, grp.ID, conversation.UpdateChatParams{Name: "支付故障应急群"})
	if err != nil || grp.Name != "支付故障应急群" {
		t.Fatalf("改群名失败：%v %+v", err, grp)
	}
	grp, err = conversation.NewAddChatParticipants(env.DB, down).Handle(ctx, subAlice, grp.ID, []string{alpha.OwnerID})
	if err != nil || grp.ParticipantCount != 3 {
		t.Fatalf("拉主管理员进群失败：%v %+v", err, grp)
	}
	if _, err := getChat.Handle(ctx, subOwner, grp.ID); err != nil {
		t.Fatalf("进群后主管理员应可见群聊：%v", err)
	}
	if err := conversation.NewLeaveChat(env.DB, down).Handle(ctx, subOwner, grp.ID); err != nil {
		t.Fatalf("主管理员退群失败：%v", err)
	}
	if _, err := getChat.Handle(ctx, subOwner, grp.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("退群后主管理员应不再可见群聊：%v", err)
	}

	// 6. 聊天搜索：支持按单聊对方姓名、群聊名、聊天记录原文及跨语言译文全文检索并返回高亮摘要
	if _, err := sendMsg.Handle(ctx, subAlice, dm1.ID, conversation.SendChatMessageBody{
		Text:        "请核实 UTR 998877 的结算状态",
		Translation: &conversation.Translation{Language: "en", Text: "Please verify settlement status for UTR 998877"},
	}); err != nil {
		t.Fatal(err)
	}
	refreshAllSearch(t, env)
	byPeer, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{CursorParams: page.CursorParams{PerPage: 20}, Q: "Bob"})
	if err != nil || len(byPeer.Items) != 2 {
		t.Fatalf("按同事姓名搜索应命中单聊和含该同事的群聊：%v %+v", err, byPeer)
	}
	byGroup, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{CursorParams: page.CursorParams{PerPage: 20}, Q: "支付故障"})
	if err != nil || len(byGroup.Items) != 1 || byGroup.Items[0].ID != grp.ID {
		t.Fatalf("按群聊名搜索异常：%v %+v", err, byGroup)
	}
	sharedWithBob, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{
		CursorParams: page.CursorParams{PerPage: 20},
		Mode:         conversation.ChatGroup,
		MemberID:     bob.ID,
	})
	if err != nil || len(sharedWithBob.Items) != 1 || sharedWithBob.Items[0].ID != grp.ID {
		t.Fatalf("按 member_id 查询共同群聊异常：%v %+v", err, sharedWithBob)
	}
	sharedWithOwner, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{
		CursorParams: page.CursorParams{PerPage: 20},
		Mode:         conversation.ChatGroup,
		MemberID:     alpha.OwnerID,
	})
	if err != nil || len(sharedWithOwner.Items) != 0 {
		t.Fatalf("已退群成员不应出现在共同群聊中：%v %+v", err, sharedWithOwner)
	}
	byMsgZh, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{CursorParams: page.CursorParams{PerPage: 20}, Q: "结算状态"})
	if err != nil || len(byMsgZh.Items) != 1 || byMsgZh.Items[0].ID != dm1.ID || byMsgZh.Items[0].Match == nil {
		t.Fatalf("按中文消息全文搜索应命中单聊并返回摘要：%v %+v", err, byMsgZh)
	}
	byMsgEn, err := listChats.Handle(ctx, subBob, &conversation.ListChatsInput{CursorParams: page.CursorParams{PerPage: 20}, Q: "settlement"})
	if err != nil || len(byMsgEn.Items) != 1 || byMsgEn.Items[0].ID != dm1.ID || byMsgEn.Items[0].Match == nil {
		t.Fatalf("按英文译文全文搜索应命中单聊并返回摘要：%v %+v", err, byMsgEn)
	}

	// 5. 边界校验：服务型与工单动作拒绝内部聊天，收件箱不混入聊天
	if _, err := conversation.NewReply(env.DB, down).Handle(ctx, subAlice, dm1.ID, conversation.ReplyBody{Text: "hi"}); !errors.Is(err, conversation.ErrWrongKind) {
		t.Fatalf("Reply 应拒绝内部聊天：%v", err)
	}
	if _, err := conversation.NewAssign(env.DB, down).Handle(ctx, subAlice, dm1.ID, conversation.Assignment{AssigneeID: &alice.ID}); !errors.Is(err, conversation.ErrWrongKind) {
		t.Fatalf("Assign 应拒绝内部聊天：%v", err)
	}
	if _, err := conversation.NewSetPriority(env.DB, down).Handle(ctx, subAlice, dm1.ID, conversation.High); !errors.Is(err, conversation.ErrWrongKind) {
		t.Fatalf("SetPriority 应拒绝内部聊天：%v", err)
	}
	if _, err := conversation.NewAddNote(env.DB, down).Handle(ctx, subAlice, dm1.ID, "note", nil); !errors.Is(err, conversation.ErrWrongKind) {
		t.Fatalf("AddNote 应拒绝内部聊天：%v", err)
	}
	inbox, err := conversation.NewGetInbox(env.DB).Handle(ctx, subAlice)
	if err != nil || inbox.All.Count != 0 || inbox.Tickets.All != 0 {
		t.Fatalf("内部聊天不应混入收件箱或工单计数：%v %+v", err, inbox)
	}
}
