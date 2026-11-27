package conversation_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/extgroup"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

type botRecordingConnector struct {
	sent []channel.Outgoing
}

func (c *botRecordingConnector) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		Transport:       channel.TransportLongConnection,
		MessageTypes:    []string{"text", "image", "file"},
		SupportsDirect:  true,
		SupportsGroup:   true,
		SupportsMention: true,
		SupportsReplyTo: true,
	}
}

func (c *botRecordingConnector) Send(_ context.Context, m channel.Outgoing) (string, bool, error) {
	c.sent = append(c.sent, m)
	return "bot-ext-" + m.ItemID, true, nil
}

func TestInternalContactsAndExternalGroupConversations(t *testing.T) {
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

	allPerms := auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))
	owner := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: allPerms}
	ownerBeta := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: allPerms}
	sys := identity.Subject{Type: identity.System, ID: "bot", Tenant: alpha.ID}

	itTeam, err := team.NewCreate(env.DB).Handle(ctx, owner, team.Changes{Name: "IT 内部支持"})
	if err != nil {
		t.Fatal(err)
	}
	wecomCh, err := channel.NewCreate(env.DB).Handle(ctx, owner, channel.CreateParams{
		Kind: channel.WeCom, Audience: channel.AudienceInternal, Name: "IT 企微机器人", TeamID: &itTeam.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	customerCh, err := channel.NewCreate(env.DB).Handle(ctx, owner, channel.CreateParams{
		Kind: channel.Test, Audience: channel.AudienceCustomer, Name: "对外客服渠道",
	})
	if err != nil {
		t.Fatal(err)
	}

	recConn := &botRecordingConnector{}
	reg := channel.DefaultRegistry()
	reg[channel.WeCom] = recConn
	down := conversation.Downstream{Roles: env.Roles, Registry: reg}
	receive := conversation.NewReceive(env.DB, reg, down)
	reply := conversation.NewReply(env.DB, down)
	deliver := conversation.NewDeliver(env.DB, reg, down)

	// 1. 内部员工 1:1 私聊机器人 -> 创建 internal 联系人，不污染默认外部客户视图，支持 member_id 绑定
	dmRec, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID:    wecomCh.ID,
		From:         "emp_alice",
		FromName:     "Alice (研发)",
		ExternalID:   "wecom-dm-1",
		Text:         "我的 VPN 连不上",
		ReplyContext: map[string]any{"req_id": "req-dm-1", "chattype": "single"},
	})
	if err != nil {
		t.Fatalf("内部 1:1 入站失败: %v", err)
	}
	dmConv, err := conversation.NewGet(env.DB).Handle(ctx, owner, dmRec.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if dmConv.Audience != channel.AudienceInternal || dmConv.Contact == nil || dmConv.Contact.Kind != contact.Internal || dmConv.ExternalGroup != nil {
		t.Fatalf("内部 1:1 会话结构不对: %+v", dmConv)
	}
	if dmConv.TeamID == nil || *dmConv.TeamID != itTeam.ID {
		t.Fatalf("新内部会话应进入渠道默认团队: %+v", dmConv.TeamID)
	}

	// 客户默认列表不显示内部联系人；传 kind=internal 可查到
	listContacts := contact.NewList(env.DB)
	defaultList, err := listContacts.Handle(ctx, owner, &contact.ListInput{})
	if err != nil || len(defaultList.Items) != 0 {
		t.Fatalf("默认客户列表不应包含内部联系人: %+v %v", defaultList, err)
	}
	internalList, err := listContacts.Handle(ctx, owner, &contact.ListInput{Kind: contact.Internal})
	if err != nil || len(internalList.Items) != 1 || internalList.Items[0].ID != dmConv.Contact.ID {
		t.Fatalf("内部联系人列表应返回 Alice: %+v %v", internalList, err)
	}
	contactSum, err := contact.NewGetSummary(env.DB).Handle(ctx, owner)
	if err != nil || contactSum.All != 0 || contactSum.Internal != 1 {
		t.Fatalf("联系人统计不对: %+v %v", contactSum, err)
	}
	// 绑定工作区成员 ID
	updContact, err := contact.NewUpdate(env.DB, env.Roles).Handle(ctx, owner, dmConv.Contact.ID, contact.UpdateBody{MemberID: &owner.ID})
	if err != nil || updContact.MemberID == nil || *updContact.MemberID != owner.ID {
		t.Fatalf("绑定成员失败: %+v %v", updContact, err)
	}

	// 2. 同一人在客户渠道也发消息 -> 外部客户线索会话与内部员工会话互相隔离
	custRec, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: customerCh.ID,
		From:      "cust_1",
		FromName:  "Customer Bob",
		Text:      "你好，咨询价格",
	})
	if err != nil || custRec.ConversationID == dmRec.ConversationID {
		t.Fatalf("客户会话应独立创建: %+v %v", custRec, err)
	}
	if _, err := reply.Handle(ctx, owner, dmRec.ConversationID, conversation.ReplyBody{
		ChannelID: customerCh.ID,
		Text:      "不能跨 audience 切渠道回复",
	}); !errors.Is(err, conversation.ErrChannelUnavailable) {
		t.Fatalf("跨 audience 切换渠道应被拒绝: %v", err)
	}

	// 3. 外部群 @ 机器人：多个发言人进入同一活跃群会话，发言人落 external_users 不写 contacts
	grpRec1, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID:      wecomCh.ID,
		From:           "emp_bob",
		FromName:       "Bob",
		ExternalID:     "wecom-grp-msg-1",
		ExternalChatID: "wr-group-ops",
		IsGroup:        true,
		ChatTitle:      "运维应急群",
		Text:           "生产库 CPU 飙高，请协助排查",
		ReplyContext:   map[string]any{"req_id": "req-grp-1", "chattype": "group", "chatid": "wr-group-ops"},
	})
	if err != nil {
		t.Fatalf("群消息 1 入站失败: %v", err)
	}
	grpRec2, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID:         wecomCh.ID,
		From:              "emp_carol",
		FromName:          "Carol",
		ExternalID:        "wecom-grp-msg-2",
		ExternalChatID:    "wr-group-ops",
		IsGroup:           true,
		ChatTitle:         "运维应急群",
		ReplyToExternalID: "wecom-grp-msg-1",
		Text:              "补充：慢查询来自报表节点",
		ReplyContext:      map[string]any{"req_id": "req-grp-2", "chattype": "group", "chatid": "wr-group-ops"},
	})
	if err != nil {
		t.Fatalf("群消息 2 入站失败: %v", err)
	}
	if grpRec1.ConversationID != grpRec2.ConversationID {
		t.Fatalf("同一外部群的两次 @ 应合并到同一活跃会话: %s vs %s", grpRec1.ConversationID, grpRec2.ConversationID)
	}

	// 确认群发言人没有污染 contacts 表
	totalContacts, err := env.DB.NewSelect().Table("contacts").Where("tenant_id = ?", alpha.ID).Count(ctx)
	if err != nil || totalContacts != 2 {
		t.Fatalf("群发言人不应写入 contacts，期望 2（Alice+Customer Bob），实际 %d (err=%v)", totalContacts, err)
	}

	grpConv, err := conversation.NewGet(env.DB).Handle(ctx, owner, grpRec1.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if grpConv.ExternalGroup == nil || grpConv.ExternalGroup.Name != "运维应急群" || grpConv.Contact != nil || len(grpConv.AvailableChannels) != 0 {
		t.Fatalf("外部群会话 DTO 不对: %+v", grpConv)
	}

	items, err := conversation.NewTimeline(env.DB).Handle(ctx, owner, grpRec1.ConversationID, 0, 50)
	if err != nil || len(items) != 2 {
		t.Fatalf("读取群会话时间线失败: %+v %v", items, err)
	}
	if items[0].Actor.Type != conversation.ExternalUserActor || items[0].Actor.Name != "Bob" || items[0].Actor.ExternalID != "emp_bob" {
		t.Fatalf("第一条群消息发言人不对: %+v", items[0].Actor)
	}
	if items[1].Actor.Name != "Carol" || items[1].ReplyToItemID == nil || *items[1].ReplyToItemID != items[0].ID {
		t.Fatalf("第二条群消息发言人或引用关系不对: %+v", items[1])
	}

	// 4. 回复群会话（带引用与 @ 发言人）并执行 Deliver，验证 Outgoing 目标、ReplyToExternalID、Mentions 与 ReplyContext
	replyItem, err := reply.Handle(ctx, owner, grpRec1.ConversationID, conversation.ReplyBody{
		Text:          "收到，正在限流报表节点",
		ReplyToItemID: items[1].ID,
		ExternalMentions: []channel.ExternalMention{
			{ExternalID: "emp_carol", Name: "Carol"},
		},
	})
	if err != nil {
		t.Fatalf("回复群会话失败: %v", err)
	}
	if err := deliver.Job(ctx, conversation.Appended{TenantID: alpha.ID, ConversationID: grpRec1.ConversationID, ItemID: replyItem.ID}); err != nil {
		t.Fatalf("投递群回复失败: %v", err)
	}
	if len(recConn.sent) != 1 {
		t.Fatalf("应投递 1 条消息，实际 %d", len(recConn.sent))
	}
	out := recConn.sent[0]
	if out.Target.Kind != channel.TargetGroup || out.Target.ExternalTo != "wr-group-ops" || out.ExternalTo != "wr-group-ops" {
		t.Fatalf("群回复投递目标不对: %+v", out.Target)
	}
	if out.ReplyToExternalID != "wecom-grp-msg-2" {
		t.Fatalf("引用外部消息 ID 不对: %q", out.ReplyToExternalID)
	}
	if len(out.Mentions) != 1 || out.Mentions[0].ExternalID != "emp_carol" {
		t.Fatalf("群回复外部 @ 不对: %+v", out.Mentions)
	}
	if out.ReplyContext["req_id"] != "req-grp-2" {
		t.Fatalf("ReplyContext 应带最近入站 req_id: %+v", out.ReplyContext)
	}

	// 5. 群会话转工单、等待客户回复自动切回 in_progress、工单广播目标包含该外部群
	ticketDTO, err := conversation.NewCreateTicket(env.DB, down).Handle(ctx, owner, conversation.CreateTicketParams{
		Title:                "报表节点慢查询治理",
		SourceConversationID: &grpRec1.ConversationID,
	})
	if err != nil {
		t.Fatalf("群会话转工单失败: %v", err)
	}
	if ticketDTO.LinkedCount != 1 || len(ticketDTO.LinkedGroups) != 1 || ticketDTO.LinkedGroups[0].Group.ID != grpConv.ExternalGroup.ID {
		t.Fatalf("工单应关联该外部群: %+v", ticketDTO)
	}
	targets, err := conversation.ResolveTicketNotifyTargets(ctx, env.DB, alpha.ID, ticketDTO.ID)
	if err != nil || len(targets) != 1 || targets[0].ExternalGroupID != grpConv.ExternalGroup.ID || targets[0].ConversationID != grpRec1.ConversationID {
		t.Fatalf("工单广播目标应包含外部群会话: %+v %v", targets, err)
	}
	if _, err := conversation.NewSetTicketStatus(env.DB, down).Handle(ctx, owner, ticketDTO.ID, conversation.TicketWaiting); err != nil {
		t.Fatal(err)
	}
	if _, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID:      wecomCh.ID,
		From:           "emp_bob",
		FromName:       "Bob",
		ExternalID:     "wecom-grp-msg-3",
		ExternalChatID: "wr-group-ops",
		IsGroup:        true,
		Text:           "已经恢复正常了",
	}); err != nil {
		t.Fatal(err)
	}
	resumed, err := conversation.NewGetTicket(env.DB).Handle(ctx, owner, ticketDTO.ID)
	if err != nil || resumed.Status != conversation.TicketInProgress {
		t.Fatalf("外部群再发言后等待中的工单应自动恢复处理中: %+v %v", resumed, err)
	}

	// 6. 机器人被移出群后禁止继续回复，且其他工作区访问不到该会话
	if err := extgroup.SyncRemoved(ctx, env.DB, alpha.ID, wecomCh.ID, "wr-group-ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := reply.Handle(ctx, owner, grpRec1.ConversationID, conversation.ReplyBody{Text: "移出后发消息"}); !errors.Is(err, extgroup.ErrRemoved) {
		t.Fatalf("已移出的外部群回复应返回 extgroup.ErrRemoved: %v", err)
	}
	if _, err := conversation.NewGet(env.DB).Handle(ctx, ownerBeta, grpRec1.ConversationID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("其他工作区不应看到 Alpha 的群会话: %v", err)
	}
}
