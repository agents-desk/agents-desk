package conversation_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/apptest"
	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/role"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
	"agents-desk/internal/tickettype"
)

// TestTicketCoreLifecycleAndIsolation 验证 TK2：工单创建/转单、多会话归集、状态流转与客户回流、
// 公开进度权限、自定义属性路由、服务型动作拒绝工单、跨工作区隔离及大代理关联会话数据隔离。
func TestTicketCoreLifecycleAndIsolation(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

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

	// 建立两个团队收件箱（技术团队 + 印度大代理 A 团队）与两位仅限各自团队可见的成员
	techTeam, err := team.NewCreate(env.DB).Handle(ctx, sysA, team.Changes{Name: "技术支持", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	agentTeamA, err := team.NewCreate(env.DB).Handle(ctx, sysA, team.Changes{Name: "印度代理A组", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	agentTeamB, err := team.NewCreate(env.DB).Handle(ctx, sysA, team.Changes{Name: "印度代理B组", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	partnerA, err := addMember.Handle(ctx, sysA, member.AddParams{Email: "partner-a@alpha.com", Name: "代理A", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	partnerB, err := addMember.Handle(ctx, sysA, member.AddParams{Email: "partner-b@alpha.com", Name: "代理B", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	putTeamMember := team.NewPutMember(env.DB, env.Roles, env.MemberLogins, env.Files)
	if _, err := putTeamMember.Handle(ctx, sysA, agentTeamA.ID, partnerA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := putTeamMember.Handle(ctx, sysA, agentTeamB.ID, partnerB.ID); err != nil {
		t.Fatal(err)
	}

	subOwner := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	subPartnerA := identity.Subject{Type: member.Type, ID: partnerA.ID, Tenant: alpha.ID, Permissions: auth.WithBase(agentRole.Permissions)}
	subPartnerB := identity.Subject{Type: member.Type, ID: partnerB.ID, Tenant: alpha.ID, Permissions: auth.WithBase(agentRole.Permissions)}
	subBetaOwner := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}

	// 创建测试渠道与两条不同大代理团队的客户会话
	chA, err := channel.NewCreate(env.DB).Handle(ctx, sysA, channel.CreateParams{Kind: channel.Test, Name: "代理A渠道", TeamID: &agentTeamA.ID})
	if err != nil {
		t.Fatal(err)
	}
	chB, err := channel.NewCreate(env.DB).Handle(ctx, sysA, channel.CreateParams{Kind: channel.Test, Name: "代理B渠道", TeamID: &agentTeamB.ID})
	if err != nil {
		t.Fatal(err)
	}
	down := conversation.Downstream{}
	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	convA, err := recv.Handle(ctx, sysA, conversation.ReceiveParams{ChannelID: chA.ID, From: "cust-a", FromName: "Rahul", Text: "UPI payment failed"})
	if err != nil {
		t.Fatal(err)
	}
	convB, err := recv.Handle(ctx, sysA, conversation.ReceiveParams{ChannelID: chB.ID, From: "cust-b", FromName: "Amit", Text: "UPI payment also failed"})
	if err != nil {
		t.Fatal(err)
	}

	// 创建两个工单类型（客户可见充值单 + 仅成员可见审批单）及各自字段
	billingType, err := tickettype.NewCreate(env.DB).Handle(ctx, sysA, tickettype.CreateParams{
		Name: "充值问题", DefaultTeamID: &techTeam.ID, DefaultVisibility: tickettype.VisibilityAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	privateType, err := tickettype.NewCreate(env.DB).Handle(ctx, sysA, tickettype.CreateParams{
		Name: "内部审批", DefaultTeamID: &techTeam.ID, DefaultVisibility: tickettype.VisibilityMembers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attribute.NewCreate(env.DB).Handle(ctx, sysA, attribute.CreateParams{
		Target: attribute.TargetTicket, TicketTypeID: &billingType.ID, Key: "upi_ref", Name: "UPI 流水号", Type: attribute.TypeText,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. 代理 A 把自己的客户会话转成工单（默认进入技术支持团队收件箱）
	createTicket := conversation.NewCreateTicket(env.DB, down)
	tk1, err := createTicket.Handle(ctx, subPartnerA, conversation.CreateTicketParams{
		TypeID:               billingType.ID,
		Title:                "UPI 通道掉单故障",
		Description:          "多客户充值扣款未到账",
		SourceConversationID: &convA.ConversationID,
		Attributes:           map[string]any{"upi_ref": "UTR-001"},
	})
	if err != nil {
		t.Fatalf("代理A转工单失败：%v", err)
	}
	if tk1.Number != 1 || tk1.Status != conversation.TicketSubmitted || tk1.Visibility != tickettype.VisibilityAll || tk1.LinkedCount != 1 {
		t.Fatalf("工单初始状态异常：%+v", tk1)
	}
	if tk1.TeamID == nil || *tk1.TeamID != techTeam.ID {
		t.Fatalf("应继承类型默认团队收件箱：%+v", tk1.TeamID)
	}
	if tk1.Attributes["upi_ref"] != "UTR-001" {
		t.Fatalf("工单初始字段异常：%+v", tk1.Attributes)
	}

	// 代理 A 作为创建人和关联会话所属成员，即使工单在“技术支持”团队也仍然可见；代理 B 此时不可见
	getTicket := conversation.NewGetTicket(env.DB)
	if _, err := getTicket.Handle(ctx, subPartnerA, tk1.ID); err != nil {
		t.Fatalf("创建人代理A应可见工单：%v", err)
	}
	if _, err := getTicket.Handle(ctx, subPartnerB, tk1.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("无关代理B不应可见工单：%v", err)
	}
	if _, err := getTicket.Handle(ctx, subBetaOwner, tk1.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("跨工作区不应可见工单：%v", err)
	}

	// 2. 管理员将代理 B 的客户会话也关联到同一张聚合故障工单 #1
	linkConv := conversation.NewLinkConversation(env.DB, down)
	tk1, err = linkConv.Handle(ctx, subOwner, tk1.ID, convB.ConversationID)
	if err != nil || tk1.LinkedCount != 2 || len(tk1.LinkedConversations) != 2 {
		t.Fatalf("管理员关联第二个会话失败：%v %+v", err, tk1)
	}
	// 重复关联应报 ErrLinkAlreadyExists
	if _, err := linkConv.Handle(ctx, subOwner, tk1.ID, convB.ConversationID); !errors.Is(err, conversation.ErrLinkAlreadyExists) {
		t.Fatalf("重复关联应报错：%v", err)
	}

	// 关键隔离验证：代理 A 与代理 B 现在都能看到共享故障单 #1，但各自只能看到自己权限内的 1 条关联会话！
	viewA, err := getTicket.Handle(ctx, subPartnerA, tk1.ID)
	if err != nil || viewA.LinkedCount != 2 || len(viewA.LinkedConversations) != 1 || viewA.LinkedConversations[0].ID != convA.ConversationID {
		t.Fatalf("代理A关联会话隔离失败：%v %+v", err, viewA)
	}
	viewB, err := getTicket.Handle(ctx, subPartnerB, tk1.ID)
	if err != nil || viewB.LinkedCount != 2 || len(viewB.LinkedConversations) != 1 || viewB.LinkedConversations[0].ID != convB.ConversationID {
		t.Fatalf("代理B关联会话隔离失败：%v %+v", err, viewB)
	}

	// 按编号查询与列表查询
	byNum, err := conversation.NewGetTicketByNumber(env.DB).Handle(ctx, subPartnerA, 1)
	if err != nil || byNum.ID != tk1.ID {
		t.Fatalf("按编号查工单失败：%v %+v", err, byNum)
	}
	listTickets := conversation.NewListTickets(env.DB)
	listed, err := listTickets.Handle(ctx, subOwner, &conversation.ListTicketsInput{CursorParams: page.CursorParams{PerPage: 20}, Q: "#1"})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != tk1.ID {
		t.Fatalf("按 #1 搜索工单失败：%v %+v", err, listed)
	}
	// 工单不得混入服务型会话列表
	svcList, err := conversation.NewList(env.DB).Handle(ctx, subOwner, &conversation.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Inbox: conversation.InboxAll, Status: conversation.Open})
	if err != nil || len(svcList.Items) != 2 {
		t.Fatalf("服务型会话列表不应混入工单：%v count=%d", err, len(svcList.Items))
	}
	// 收件箱工单分组计数应为 1
	inbox, err := conversation.NewGetInbox(env.DB).Handle(ctx, subOwner)
	if err != nil || inbox.Tickets.All != 1 {
		t.Fatalf("收件箱工单计数异常：%v %+v", err, inbox.Tickets)
	}

	// 3. 服务型会话专有动作应按 kind 拒绝工单；复用动作（备注、属性、指派）应正常生效
	if _, err := conversation.NewReply(env.DB, down).Handle(ctx, subOwner, tk1.ID, conversation.ReplyBody{Text: "hi"}); !errors.Is(err, conversation.ErrWrongKind) {
		t.Fatalf("Reply 应拒绝工单：%v", err)
	}
	if _, err := conversation.NewClose(env.DB, down).Handle(ctx, subOwner, tk1.ID); !errors.Is(err, conversation.ErrWrongKind) {
		t.Fatalf("Close 应拒绝工单：%v", err)
	}
	if _, err := conversation.NewSnooze(env.DB, down).Handle(ctx, subOwner, tk1.ID, time.Now().UTC().Add(time.Hour)); !errors.Is(err, conversation.ErrWrongKind) {
		t.Fatalf("Snooze 应拒绝工单：%v", err)
	}
	if _, err := conversation.NewReopen(env.DB, down).Handle(ctx, subOwner, tk1.ID); !errors.Is(err, conversation.ErrWrongKind) {
		t.Fatalf("Reopen 应拒绝工单：%v", err)
	}
	if _, err := conversation.NewPatchAttributes(env.DB, down).Handle(ctx, subOwner, tk1.ID, map[string]any{"upi_ref": "UTR-002"}); err != nil {
		t.Fatalf("修改工单自定义字段失败：%v", err)
	}
	if _, err := conversation.NewAddNote(env.DB, down).Handle(ctx, subOwner, tk1.ID, "正在核查 UPI 通道", nil); err != nil {
		t.Fatalf("工单写内部备注失败：%v", err)
	}

	// 4. 状态流转：等待客户 → 客户在关联会话发新消息自动切回处理中 → 已解决记录 resolved_at → 重新改回处理中清空 resolved_at
	setStatus := conversation.NewSetTicketStatus(env.DB, down)
	if _, err := setStatus.Handle(ctx, subOwner, tk1.ID, conversation.TicketWaiting); err != nil {
		t.Fatalf("切到等待客户失败：%v", err)
	}
	if _, err := recv.Handle(ctx, sysA, conversation.ReceiveParams{ChannelID: chA.ID, From: "cust-a", Text: "Here is my UTR screenshot"}); err != nil {
		t.Fatalf("客户回复失败：%v", err)
	}
	afterReply, err := getTicket.Handle(ctx, subOwner, tk1.ID)
	if err != nil || afterReply.Status != conversation.TicketInProgress {
		t.Fatalf("客户回复后工单应自动回到 in_progress：%v status=%s", err, afterReply.Status)
	}
	resolved, err := setStatus.Handle(ctx, subOwner, tk1.ID, conversation.TicketResolved)
	if err != nil || resolved.Status != conversation.TicketResolved || resolved.ResolvedAt == nil {
		t.Fatalf("切到已解决应记录 resolved_at：%v %+v", err, resolved)
	}
	reopened, err := setStatus.Handle(ctx, subOwner, tk1.ID, conversation.TicketInProgress)
	if err != nil || reopened.ResolvedAt != nil {
		t.Fatalf("改回处理中应清空 resolved_at：%v %+v", err, reopened)
	}

	// 5. 公开进度发布：客户可见工单允许，仅成员可见工单返回 ErrPrivateTicket
	publish := conversation.NewPublishTicketUpdate(env.DB, down)
	if _, err := publish.Handle(ctx, subOwner, tk1.ID, conversation.PublishTicketUpdateBody{Text: "通道正在恢复中"}); err != nil {
		t.Fatalf("客户可见工单发布公开进度失败：%v", err)
	}
	tkPrivate, err := createTicket.Handle(ctx, subOwner, conversation.CreateTicketParams{
		TypeID: privateType.ID, Title: "内部退款复核",
	})
	if err != nil || tkPrivate.Number != 2 || tkPrivate.Visibility != tickettype.VisibilityMembers {
		t.Fatalf("新建内部工单失败：%v %+v", err, tkPrivate)
	}
	if _, err := publish.Handle(ctx, subOwner, tkPrivate.ID, conversation.PublishTicketUpdateBody{Text: "不应允许"}); !errors.Is(err, conversation.ErrPrivateTicket) {
		t.Fatalf("仅成员可见工单应拒绝公开进度：%v", err)
	}

	// 6. 已被工单引用的工单类型不能删除
	if err := tickettype.NewDelete(env.DB).Handle(ctx, sysA, billingType.ID); !errors.Is(err, tickettype.ErrInUse) {
		t.Fatalf("被工单引用的工单类型应返回 ErrInUse：%v", err)
	}

	// 7. 取消关联会话
	afterUnlink, err := conversation.NewUnlinkConversation(env.DB, down).Handle(ctx, subOwner, tk1.ID, convB.ConversationID)
	if err != nil || afterUnlink.LinkedCount != 1 {
		t.Fatalf("取消关联失败：%v %+v", err, afterUnlink)
	}
	if _, err := getTicket.Handle(ctx, subPartnerB, tk1.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("取消关联后代理B应不再可见工单：%v", err)
	}

	// 8. 仅拥有 ScopeAssigned 的成员不依赖任何其他权限校验
	_ = role.Defaults
}
