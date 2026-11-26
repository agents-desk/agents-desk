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
)

// omniStubConnector 是测试多渠道投递的桩连接器，可配置多会话能力并记录发出消息。
type omniStubConnector struct {
	multi bool
	sent  []channel.Outgoing
}

func (c *omniStubConnector) Capabilities() channel.Capabilities {
	return channel.Capabilities{MessageTypes: []string{"text"}, MultipleConversations: c.multi}
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

	widgetConn := &omniStubConnector{multi: true}
	tgConn := &omniStubConnector{multi: false}
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

	// 2. 同一用户紧接着在 Telegram 发消息：应自动归入同一条会话
	r2, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: tgCh.ID, From: "tg-100", Text: "TG 追问：订单号 888",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r2.ConversationID != r1.ConversationID {
		t.Fatalf("期望跨渠道自动归入同一会话 %s，实际新建了 %s", r1.ConversationID, r2.ConversationID)
	}

	// 3. 检查会话 DTO：来源渠道为官网、默认回复渠道自动更新为 Telegram，Channels 包含两者，AvailableChannels 包含两者
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

	// 8. 客户侧（网页聊天插件）：已登录用户能看到该跨渠道会话及来自 Telegram 的消息，并能继续发消息
	contactSub := identity.Subject{Type: contact.Type, ID: user.ID, Tenant: tenantID}
	cList, err := conversation.NewListForContact(env.DB).Handle(ctx, contactSub, widgetCh.ID)
	if err != nil || len(cList) != 1 || cList[0].ID != r1.ConversationID {
		t.Fatalf("已登录用户在插件应看到跨渠道会话：len=%d err=%v", len(cList), err)
	}
	cItems, err := conversation.NewTimelineForContact(env.DB).Handle(ctx, contactSub, widgetCh.ID, r1.ConversationID, 0, 50)
	if err != nil || len(cItems) != 4 {
		t.Fatalf("已登录用户时间线应包含 4 条跨渠道消息：len=%d err=%v", len(cItems), err)
	}
	if cItems[1].Channel == nil || cItems[1].Channel.ID != tgCh.ID {
		t.Fatalf("第 2 条消息应标记来自 Telegram，实际 %+v", cItems[1].Channel)
	}
	r3, err := receive.Handle(ctx, contactSub, conversation.ReceiveParams{
		ChannelID: widgetCh.ID, From: "w-u100", ConversationID: r1.ConversationID, Text: "我在网页看到了，谢谢！",
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

func TestConversationMergeAndContactMergeFlow(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	f := setup(t, env, "beta")
	other := setup(t, env, "other")
	tenantID, agent, sys := f.system.Tenant, f.agent, f.system

	reg := channel.Registry{
		channel.Widget:   &omniStubConnector{multi: true},
		channel.Telegram: &omniStubConnector{multi: false},
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
	mergeConv := conversation.NewMerge(env.DB, down)
	mergeContact := contact.NewMerge(env.DB, conversation.OnContactMerged(down))

	// 创建两个独立线索在两个渠道的会话
	c1, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: widgetCh.ID, From: "guest-1", FromName: "林小雨（官网）", Text: "官网消息 1"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: tgCh.ID, From: "tg-guest-1", FromName: "林小雨（TG）", Text: "TG 消息 1"})
	if err != nil {
		t.Fatal(err)
	}

	// 不同联系人的两个会话不能直接合并
	if _, err := mergeConv.Handle(ctx, agent, c1.ConversationID, c2.ConversationID); !errors.Is(err, conversation.ErrMergeDifferentContact) {
		t.Fatalf("不同联系人的会话应返回 ErrMergeDifferentContact，实际 %v", err)
	}
	// 合并自己返回 ErrMergeSelf
	if _, err := mergeConv.Handle(ctx, agent, c1.ConversationID, c1.ConversationID); !errors.Is(err, conversation.ErrMergeSelf) {
		t.Fatalf("合并自己应返回 ErrMergeSelf，实际 %v", err)
	}
	// 跨工作区访问不到
	if _, err := mergeConv.Handle(ctx, other.agent, c1.ConversationID, c2.ConversationID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("其他工作区应返回 ErrNotFound，实际 %v", err)
	}

	// 读取两个联系人 ID
	conv1, err := conversation.NewGet(env.DB).Handle(ctx, agent, c1.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	conv2, err := conversation.NewGet(env.DB).Handle(ctx, agent, c2.ConversationID)
	if err != nil {
		t.Fatal(err)
	}

	// 手动合并联系人，并开启 merge_active_conversations=true：两个进行中跨渠道会话应自动合流成一条
	mergedContact, err := mergeContact.Handle(ctx, agent, conv1.Contact.ID, contact.MergeBody{
		SourceID:                 conv2.Contact.ID,
		MergeActiveConversations: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mergedContact.Identities) != 2 {
		t.Fatalf("合并后应保留 2 个渠道身份，实际 %d", len(mergedContact.Identities))
	}
	if mergedContact.ConversationCount != 1 {
		t.Fatalf("勾选同时合并进行中会话后，联系人应只剩 1 个合并会话，实际 %d", mergedContact.ConversationCount)
	}

	// 验证留存会话里包含两条渠道的消息和一条 conversation.merged 系统事件，且 seq 连续递增
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

	// 同联系人手动合并两个会话（通过多会话插件新建第二条再手动合并）
	secondConv, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: widgetCh.ID, From: "guest-1", Text: "新开一条并行会话",
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondConv.ConversationID == surviving.ID {
		t.Fatalf("多会话模式未指定 ConversationID 时应新建会话")
	}
	afterManual, err := mergeConv.Handle(ctx, agent, surviving.ID, secondConv.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if afterManual.ID != surviving.ID {
		t.Fatalf("目标会话 ID 不符：%s", afterManual.ID)
	}
	count, err := env.DB.NewSelect().Table("conversations").Where("tenant_id = ? AND contact_id = ?", tenantID, mergedContact.ID).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("手动合并后应只剩 1 条会话，实际 %d err=%v", count, err)
	}
}
