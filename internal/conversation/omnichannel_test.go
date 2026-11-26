package conversation_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/tickettype"
)

// omniStubConnector 是测试多渠道投递的桩连接器，记录发出消息。
type omniStubConnector struct {
	sent []channel.Outgoing
}

func (c *omniStubConnector) Capabilities() channel.Capabilities {
	return channel.Capabilities{MessageTypes: []string{"text"}}
}

func (c *omniStubConnector) Send(_ context.Context, m channel.Outgoing) (string, bool, error) {
	c.sent = append(c.sent, m)
	return "ext-" + m.ItemID, true, nil
}

func TestOmnichannelAutoMergeReplyAndContactView(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	f := setup(t, env, "acme")
	tenantID, agent, sys := f.system.Tenant, f.agent, f.system

	widgetConn := &omniStubConnector{}
	tgConn := &omniStubConnector{}
	reg := channel.Registry{
		channel.Widget:   widgetConn,
		channel.Telegram: tgConn,
		channel.Test:     channel.TestConnector{},
	}
	down := conversation.Downstream{Topics: []queue.Topic[conversation.Appended]{conversation.DeliverTopic}}

	// 创建两个不同渠道：网页聊天插件 + Telegram 机器人
	widgetCh, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Widget, Name: "官网"})
	if err != nil {
		t.Fatal(err)
	}
	tgCh, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Telegram, Name: "Telegram 客服"})
	if err != nil {
		t.Fatal(err)
	}

	// 将两个渠道身份关联到同一个已登录业务用户 u-100
	var user *contact.Contact
	if err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		user, err = contact.ResolveUser(ctx, tx, tenantID, widgetCh.ID, "w-u100", "u-100", contact.Profile{Name: "王小明", Email: "wxm@example.com"})
		if err != nil {
			return err
		}
		_, err = contact.ResolveUser(ctx, tx, tenantID, tgCh.ID, "tg-100", "u-100", contact.Profile{Name: "王小明"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	receive := conversation.NewReceive(env.DB, reg, down)
	reply := conversation.NewReply(env.DB, down)
	deliver := conversation.NewDeliver(env.DB, reg)
	get := conversation.NewGet(env.DB)
	timeline := conversation.NewTimeline(env.DB)

	// 1. 用户先在官网发起会话
	r1, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: widgetCh.ID, From: "w-u100", Text: "官网提问：订单怎么查？",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. 同一用户紧接着在 Telegram 发消息：应自动归入同一条活跃会话
	r2, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: tgCh.ID, From: "tg-100", Text: "TG 追问：订单号 888",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r2.ConversationID != r1.ConversationID {
		t.Fatalf("期望跨渠道自动归入同一会话 %s，实际新建了 %s", r1.ConversationID, r2.ConversationID)
	}

	// 3. 检查会话 DTO：来源渠道为官网、默认回复渠道自动更新为 Telegram，Channels 与 AvailableChannels 均包含两者
	dto, err := get.Handle(ctx, agent, r1.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if dto.Channel == nil || dto.Channel.ID != widgetCh.ID {
		t.Fatalf("来源渠道应为官网，实际 %+v", dto.Channel)
	}
	if dto.ReplyChannel == nil || dto.ReplyChannel.ID != tgCh.ID {
		t.Fatalf("默认回复渠道应为 Telegram，实际 %+v", dto.ReplyChannel)
	}
	if len(dto.Channels) != 2 || len(dto.AvailableChannels) != 2 {
		t.Fatalf("期望 Channels=2 且 AvailableChannels=2，实际 %d / %d", len(dto.Channels), len(dto.AvailableChannels))
	}

	// 4. 坐席默认回复（不传 channel_id）应走 Telegram 投递
	rep1, err := reply.Handle(ctx, agent, r1.ConversationID, conversation.ReplyBody{Text: "收到，正在查 888"})
	if err != nil {
		t.Fatal(err)
	}
	if rep1.Channel == nil || rep1.Channel.ID != tgCh.ID {
		t.Fatalf("默认回复消息应标记为 Telegram 渠道，实际 %+v", rep1.Channel)
	}
	if err := deliver.Job(ctx, conversation.Appended{TenantID: tenantID, ConversationID: r1.ConversationID, ItemID: rep1.ID, Seq: rep1.Seq}); err != nil {
		t.Fatal(err)
	}
	if len(tgConn.sent) != 1 || tgConn.sent[0].ExternalTo != "tg-100" {
		t.Fatalf("Telegram 投递异常：%+v", tgConn.sent)
	}

	// 5. 坐席显式切换回复渠道为官网
	rep2, err := reply.Handle(ctx, agent, r1.ConversationID, conversation.ReplyBody{ChannelID: widgetCh.ID, Text: "已同步发到网页端"})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Channel == nil || rep2.Channel.ID != widgetCh.ID {
		t.Fatalf("切换渠道后的回复应标记为官网渠道，实际 %+v", rep2.Channel)
	}
	if err := deliver.Job(ctx, conversation.Appended{TenantID: tenantID, ConversationID: r1.ConversationID, ItemID: rep2.ID, Seq: rep2.Seq}); err != nil {
		t.Fatal(err)
	}
	if len(widgetConn.sent) != 1 || widgetConn.sent[0].ExternalTo != "w-u100" {
		t.Fatalf("官网投递异常：%+v", widgetConn.sent)
	}

	// 6. 尝试回复到一个该联系人未绑定的渠道，应返回 ErrChannelUnavailable
	otherCh, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "未绑定渠道"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reply.Handle(ctx, agent, r1.ConversationID, conversation.ReplyBody{ChannelID: otherCh.ID, Text: "不应该发出去"}); !errors.Is(err, conversation.ErrChannelUnavailable) {
		t.Fatalf("未绑定渠道应返回 ErrChannelUnavailable，实际 %v", err)
	}

	// 7. 时间线中每条消息均携带各自所属渠道，按 Telegram 渠道筛选也能命中该会话
	items, err := timeline.Handle(ctx, agent, r1.ConversationID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 4 {
		t.Fatalf("时间线至少应有 4 条消息，实际 %d", len(items))
	}
	listRes, err := conversation.NewList(env.DB).Handle(ctx, agent, &conversation.ListInput{
		Inbox: conversation.InboxAll, Filter: `{"channel_ids":["` + tgCh.ID + `"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listRes.Items) != 1 || listRes.Items[0].ID != r1.ConversationID {
		t.Fatalf("按 Telegram 筛选应命中跨渠道会话，实际 %d 条", len(listRes.Items))
	}

	// 8. 客户侧（网页聊天插件）：已登录用户在连续时间线中能看到跨渠道消息，并自动归入当前活跃会话
	contactSub := identity.Subject{Type: contact.Type, ID: user.ID, Tenant: tenantID}
	cStream, err := conversation.NewContactStream(env.DB).Handle(ctx, contactSub, widgetCh.ID, "", "", 50)
	if err != nil || len(cStream.Items) != 4 || cStream.LatestConversation == nil || cStream.LatestConversation.ID != r1.ConversationID {
		t.Fatalf("已登录用户连续时间线应包含 4 条跨渠道消息：%+v err=%v", cStream, err)
	}
	if cStream.Items[1].Channel == nil || cStream.Items[1].Channel.ID != tgCh.ID {
		t.Fatalf("第 2 条消息应标记来自 Telegram，实际 %+v", cStream.Items[1].Channel)
	}
	r3, err := receive.Handle(ctx, contactSub, conversation.ReceiveParams{
		ChannelID: widgetCh.ID, From: "w-u100", Text: "我在网页看到了，谢谢！",
	})
	if err != nil || r3.ConversationID != r1.ConversationID {
		t.Fatalf("已登录用户在插件继续跨渠道会话失败：%v", err)
	}

	// 9. 访客线索（Lead）严格单渠道隔离：在两个不同渠道不会自动合并，也看不到彼此渠道的会话
	l1, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: widgetCh.ID, From: "lead-a", Text: "访客在官网提问"})
	if err != nil {
		t.Fatal(err)
	}
	l2, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: tgCh.ID, From: "lead-a", Text: "同名外部 ID 在 TG 提问"})
	if err != nil {
		t.Fatal(err)
	}
	if l1.ConversationID == l2.ConversationID {
		t.Fatalf("未认证线索不应跨渠道合并会话")
	}
}

func TestContactMergeAutoMergesActiveConversations(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	f := setup(t, env, "beta")
	agent, sys := f.agent, f.system

	reg := channel.Registry{
		channel.Widget:   &omniStubConnector{},
		channel.Telegram: &omniStubConnector{},
	}
	down := conversation.Downstream{Topics: []queue.Topic[conversation.Appended]{conversation.DeliverTopic}}

	widgetCh, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Widget, Name: "官网"})
	if err != nil {
		t.Fatal(err)
	}
	tgCh, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Telegram, Name: "Telegram"})
	if err != nil {
		t.Fatal(err)
	}

	receive := conversation.NewReceive(env.DB, reg, down)
	mergeContact := contact.NewMerge(env.DB, conversation.OnContactMerge(down))

	// 创建两个独立线索在两个渠道各自的活跃会话
	c1, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: widgetCh.ID, From: "guest-1", FromName: "林小雨（官网）", Text: "官网消息 1"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: tgCh.ID, From: "tg-guest-1", FromName: "林小雨（TG）", Text: "TG 消息 1"})
	if err != nil {
		t.Fatal(err)
	}

	conv1, err := conversation.NewGet(env.DB).Handle(ctx, agent, c1.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	conv2, err := conversation.NewGet(env.DB).Handle(ctx, agent, c2.ConversationID)
	if err != nil {
		t.Fatal(err)
	}

	// 手动合并联系人：事务内自动先归并双活会话，满足唯一活跃约束
	mergedContact, err := mergeContact.Handle(ctx, agent, conv1.Contact.ID, contact.MergeBody{
		SourceID: conv2.Contact.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mergedContact.Identities) != 2 {
		t.Fatalf("合并后应保留 2 个渠道身份，实际 %d", len(mergedContact.Identities))
	}
	if mergedContact.ConversationCount != 1 {
		t.Fatalf("合并联系人后双活会话应自动归并为 1 条，实际 %d", mergedContact.ConversationCount)
	}

	listRes, err := conversation.NewList(env.DB).Handle(ctx, agent, &conversation.ListInput{
		Inbox: conversation.InboxAll, ContactID: mergedContact.ID,
	})
	if err != nil || len(listRes.Items) != 1 {
		t.Fatalf("合并后应只有 1 条会话：len=%d err=%v", len(listRes.Items), err)
	}
	surviving := listRes.Items[0]
	if len(surviving.Channels) != 2 || len(surviving.AvailableChannels) != 2 {
		t.Fatalf("合并后会话应显示双渠道：channels=%+v available=%+v", surviving.Channels, surviving.AvailableChannels)
	}
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, agent, surviving.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("合并后时间线应有 2 条消息 + 1 条合并事件 = 3 项，实际 %d", len(items))
	}
	for i, it := range items {
		if it.Seq != int64(i+1) {
			t.Fatalf("时间线序号应连续递增，索引 %d 实际 seq=%d", i, it.Seq)
		}
	}
	if items[2].Kind != conversation.Event || items[2].Content.Event != "conversation.merged" {
		t.Fatalf("末尾应为 conversation.merged 事件，实际 %+v", items[2])
	}

	// 场景 2：LinkUser 自动将存在活跃会话的线索合并到已存在活跃会话的用户，同样满足唯一活跃约束
	uWidget, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: widgetCh.ID, From: "u-link-web", FromName: "张三", Text: "官网已登录消息"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE contacts SET kind = 'user', external_user_id = 'ext-zhangsan' WHERE id = (SELECT contact_id FROM conversations WHERE id = ?)", uWidget.ConversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	leadTG, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: tgCh.ID, From: "tg-lead-zs", FromName: "张三 TG", Text: "TG 匿名消息"})
	if err != nil {
		t.Fatal(err)
	}
	if leadTG.ConversationID == uWidget.ConversationID {
		t.Fatalf("未绑定前应是两条会话")
	}
	if err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		_, _, err := contact.LinkUser(ctx, tx, sys.Tenant, tgCh.ID, "tg-lead-zs", "ext-zhangsan", contact.Profile{Name: "张三"}, conversation.BeforeContactMerge(down, sys))
		return err
	}); err != nil {
		t.Fatalf("LinkUser 自动归并双活会话失败：%v", err)
	}
	gotMerged, err := conversation.NewTimeline(env.DB).Handle(ctx, agent, uWidget.ConversationID, 0, 50)
	if err != nil || len(gotMerged) != 3 {
		t.Fatalf("LinkUser 后主活跃会话应归并 2 条消息 + 1 条合并事件，实际 len=%d err=%v", len(gotMerged), err)
	}
}

func TestTicketNotifyTargetRoutingAndCrossConversationWakeup(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	f := setup(t, env, "gamma")
	tenantID, agent, sys := f.system.Tenant, f.agent, f.system

	// 设置重开窗口为 0，方便模拟超过重开窗口后同一客户新开下一轮会话
	if _, err := env.DB.NewRaw("UPDATE tenants SET reopen_window_hours = 0 WHERE id = ?", tenantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	reg := channel.Registry{channel.Widget: &omniStubConnector{}}
	down := conversation.Downstream{}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Widget, Name: "App 客服"})
	if err != nil {
		t.Fatal(err)
	}
	tt, err := tickettype.NewCreate(env.DB).Handle(ctx, sys, tickettype.CreateParams{
		Name: "充值问题", DefaultVisibility: tickettype.VisibilityAll,
	})
	if err != nil {
		t.Fatal(err)
	}

	receive := conversation.NewReceive(env.DB, reg, down)
	closeConv := conversation.NewClose(env.DB, down)
	createTicket := conversation.NewCreateTicket(env.DB, down)
	setStatus := conversation.NewSetTicketStatus(env.DB, down)

	// 1. 客户在会话 C1 提问并转成工单 T1（设为 waiting_on_customer），随后关闭 C1
	c1, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "u-99", Text: "充值未到账"})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := createTicket.Handle(ctx, sys, conversation.CreateTicketParams{
		TypeID: tt.ID, Title: "充值掉单", SourceConversationID: &c1.ConversationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setStatus.Handle(ctx, agent, tk.ID, conversation.TicketWaiting); err != nil {
		t.Fatal(err)
	}
	if _, err := closeConv.Handle(ctx, agent, c1.ConversationID); err != nil {
		t.Fatal(err)
	}

	// 无活跃会话时，工单通知路由回退到关联的 C1
	targets1, err := conversation.ResolveTicketNotifyTargets(ctx, env.DB, tenantID, tk.ID)
	if err != nil || len(targets1) != 1 || targets1[0].ConversationID != c1.ConversationID {
		t.Fatalf("无活跃会话时应回退到关联会话 C1：%+v err=%v", targets1, err)
	}

	// 2. 客户再次发消息开启新一轮活跃会话 C2，且未指定 TicketID：
	//    - C2 应新建；
	//    - 挂在旧会话 C1 上的等待工单 T1 应被自动唤醒回 in_progress；
	//    - ResolveTicketNotifyTargets 应路由到当前活跃会话 C2（而非已关闭的 C1）且只返回 1 条
	c2, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "u-99", Text: "我补发一下付款截图"})
	if err != nil || c2.ConversationID == c1.ConversationID {
		t.Fatalf("应开启新会话 C2：%+v err=%v", c2, err)
	}
	gotTk, err := conversation.NewGetTicket(env.DB).Handle(ctx, agent, tk.ID)
	if err != nil || gotTk.Status != conversation.TicketInProgress {
		t.Fatalf("客户在新会话回复应自动唤醒旧会话关联的等待工单：status=%s err=%v", gotTk.Status, err)
	}
	targets2, err := conversation.ResolveTicketNotifyTargets(ctx, env.DB, tenantID, tk.ID)
	if err != nil || len(targets2) != 1 || targets2[0].ConversationID != c2.ConversationID {
		t.Fatalf("有活跃会话 C2 时工单通知应路由到 C2 且按客户去重：%+v err=%v", targets2, err)
	}

	// 3. 把工单再次切回 waiting_on_customer 并显式传 TicketID 回复：应自动把 C2 也关联到 T1 并唤醒 T1，且通知依然按客户去重为 1 条
	if _, err := setStatus.Handle(ctx, agent, tk.ID, conversation.TicketWaiting); err != nil {
		t.Fatal(err)
	}
	if _, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "u-99", Text: "这是工单补充信息", TicketID: tk.ID,
	}); err != nil {
		t.Fatal(err)
	}
	gotTk2, err := conversation.NewGetTicket(env.DB).Handle(ctx, agent, tk.ID)
	if err != nil || gotTk2.Status != conversation.TicketInProgress || gotTk2.LinkedCount != 2 {
		t.Fatalf("显式指定 TicketID 后应关联 C2 并唤醒工单：status=%s links=%d err=%v", gotTk2.Status, gotTk2.LinkedCount, err)
	}
	targets3, err := conversation.ResolveTicketNotifyTargets(ctx, env.DB, tenantID, tk.ID)
	if err != nil || len(targets3) != 1 || targets3[0].ConversationID != c2.ConversationID {
		t.Fatalf("同客户两条会话都关联同一工单时，通知仍应去重为 1 条且指向活跃会话 C2：%+v err=%v", targets3, err)
	}
}
