package widget

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/tickettype"
)

// TestContactStreamPaginationReadTypingAndTicketWakeup 验证客户连续时间线整套接口：
//   - 已登录用户跨多轮历史会话与跨渠道（网页插件 + 外部渠道）无缝拉取，支持 before 向上翻页与 after 增量拉取；
//   - Conversations 字典包含各轮次状态与评价态，LatestConversation 与 Unread 统计准确；
//   - MarkAllReadByContact 批量清零跨会话未读数；
//   - TypingOnActiveByContact 仅在存在活跃会话时向坐席广播正在输入，全部关闭时静默忽略；
//   - SendMessage 携带 TicketID 自动关联当前新活跃会话并唤醒挂在旧会话上的等待工单；
//   - 访客线索严格单渠道隔离，非联系人主体返回 ErrForbidden。
func TestContactStreamPaginationReadTypingAndTicketWakeup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "stream")

	// 将重开窗口设为 0，便于关闭后立即切出下一轮会话以验证跨会话连续时间线
	if _, err := env.DB.NewRaw("UPDATE tenants SET reopen_window_hours = 0 WHERE id = ?", f.system.Tenant).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	registry := channel.Registry{channel.Widget: NewConnector(hub), channel.Test: channel.TestConnector{}}
	down := conversation.Downstream{
		Topics:      []queue.Topic[conversation.Appended]{conversation.DeliverTopic},
		Hub:         hub,
		Roles:       env.Roles,
		ContactPush: Push(hub),
	}

	inst, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com").Handle(ctx, f.agent, f.widget.ID)
	if err != nil {
		t.Fatal(err)
	}
	tok := sign(t, inst.IdentitySecret, userClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "stream-user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		Name:             "陈明",
		Email:            "chenming@example.com",
	}, jwt.SigningMethodHS256)
	sess, userSub := f.session(t, SessionBody{UserToken: tok})

	// 为同一用户绑定另一个外部渠道（例如 Telegram）
	otherCh := &channel.Channel{
		ID: newID(), TenantID: f.system.Tenant, Kind: channel.Test, Name: "Telegram", Enabled: true, Settings: map[string]any{},
	}
	if _, err := env.DB.NewInsert().Model(otherCh).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := contact.ResolveUser(ctx, env.DB, f.system.Tenant, otherCh.ID, "tg-cm-1", "stream-user-1", contact.Profile{Name: "陈明"}); err != nil {
		t.Fatal(err)
	}
	tt, err := tickettype.NewCreate(env.DB).Handle(ctx, f.agent, tickettype.CreateParams{
		Name: "退款问题", DefaultVisibility: tickettype.VisibilityAll,
	})
	if err != nil {
		t.Fatal(err)
	}

	recv := conversation.NewReceive(env.DB, registry, down)
	send := NewSendMessage(env.DB, recv)
	reply := conversation.NewReply(env.DB, down)
	closeConv := conversation.NewClose(env.DB, down)
	reqRating := conversation.NewRequestRating(env.DB, down)
	createTicket := conversation.NewCreateTicket(env.DB, down)
	setStatus := conversation.NewSetTicketStatus(env.DB, down)
	stream := conversation.NewContactStream(env.DB)
	markAllRead := conversation.NewMarkAllReadByContact(env.DB, down)
	typingActive := conversation.NewTypingOnActiveByContact(env.DB, down)

	in := f.in(t, f.widget.ID)

	// 第 1 轮（外部渠道）：客户提问 -> 坐席回复 -> 转工单并设为等待客户 -> 关闭并请求评价
	r1, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{ChannelID: otherCh.ID, From: "tg-cm-1", Text: "第1轮外部提问"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reply.Handle(ctx, f.agent, r1.ConversationID, conversation.ReplyBody{Text: "第1轮坐席回复"}); err != nil {
		t.Fatal(err)
	}
	tk, err := createTicket.Handle(ctx, f.system, conversation.CreateTicketParams{
		TypeID: &tt.ID, Title: "退款核对", SourceConversationID: &r1.ConversationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setStatus.Handle(ctx, f.agent, tk.ID, conversation.TicketWaiting); err != nil {
		t.Fatal(err)
	}
	if _, err := closeConv.Handle(ctx, f.agent, r1.ConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := reqRating.Handle(ctx, f.system, r1.ConversationID); err != nil {
		t.Fatal(err)
	}

	// 全部会话均已关闭时，TypingOnActiveByContact 静默成功不报错
	if err := typingActive.Handle(ctx, userSub, f.widget.ID); err != nil {
		t.Fatalf("无活跃会话时正在输入应静默返回 nil：%v", err)
	}

	// 第 2 轮（网页插件）：携带 TicketID 发消息 -> 开启新活跃会话 C2、关联并唤醒工单 -> 坐席回复
	s2, err := send.Handle(in, userSub, MessageBody{Text: "第2轮补充退款信息", TicketID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if s2.ConversationID == r1.ConversationID {
		t.Fatalf("重开窗口为 0 应新建第 2 轮会话")
	}
	if s2.Item.ConversationID != s2.ConversationID {
		t.Fatalf("发消息返回项应带 conversation_id=%s，实际 %q", s2.ConversationID, s2.Item.ConversationID)
	}
	gotTk, err := conversation.NewGetTicket(env.DB).Handle(ctx, f.agent, tk.ID)
	if err != nil || gotTk.Status != conversation.TicketInProgress || gotTk.LinkedCount != 1 {
		t.Fatalf("携带 TicketID 发消息后工单应按客户去重关联并切回 in_progress：status=%s links=%d err=%v", gotTk.Status, gotTk.LinkedCount, err)
	}
	rep2, err := reply.Handle(ctx, f.agent, s2.ConversationID, conversation.ReplyBody{Text: "第2轮坐席回复"})
	if err != nil {
		t.Fatal(err)
	}

	// 1. 拉取最新一页（limit=2）：应返回第 2 轮 2 条消息，has_more=true，unread=2（第 1 轮 + 第 2 轮各 1 条客服消息）
	page1, err := stream.Handle(ctx, userSub, f.widget.ID, "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 2 || !page1.HasMore {
		t.Fatalf("第 1 页应返回 2 条且 has_more=true：len=%d hasMore=%v", len(page1.Items), page1.HasMore)
	}
	if page1.Items[0].Text != "第2轮补充退款信息" || page1.Items[1].Text != "第2轮坐席回复" {
		t.Fatalf("第 1 页消息顺序异常：%+v", page1.Items)
	}
	if page1.Unread != 2 {
		t.Fatalf("跨两轮应统计到 2 条未读，实际 %d", page1.Unread)
	}
	if page1.LatestConversation == nil || page1.LatestConversation.ID != s2.ConversationID || page1.LatestConversation.Status != conversation.Open {
		t.Fatalf("LatestConversation 异常：%+v", page1.LatestConversation)
	}

	// 2. 用 before 游标向上翻页：应取到第 1 轮（外部渠道）的 2 条消息，has_more=false，并附带第 1 轮关闭与评价态元数据
	page2, err := stream.Handle(ctx, userSub, f.widget.ID, page1.Items[0].ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 2 || page2.HasMore {
		t.Fatalf("向上翻页应返回第 1 轮 2 条且 has_more=false：len=%d hasMore=%v", len(page2.Items), page2.HasMore)
	}
	if page2.Items[0].Text != "第1轮外部提问" || page2.Items[0].Channel == nil || page2.Items[0].Channel.ID != otherCh.ID {
		t.Fatalf("第 1 轮消息应保留外部渠道标记：%+v", page2.Items[0])
	}
	meta1, ok := page2.Conversations[r1.ConversationID]
	if !ok || meta1.Status != conversation.Closed || !meta1.RatingRequested {
		t.Fatalf("第 1 轮会话元数据应标记已关闭且可评价：ok=%v meta=%+v", ok, meta1)
	}

	// 3. after 增量拉取：追加新消息后，用 rep2.ID 作为 after 游标只拉到增量项
	s3, err := send.Handle(in, userSub, MessageBody{Text: "第2轮追问"})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := stream.Handle(ctx, userSub, f.widget.ID, "", rep2.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Items) != 1 || delta.Items[0].ID != s3.Item.ID {
		t.Fatalf("after 增量拉取应只返回最新 1 条：%+v", delta.Items)
	}

	// 4. MarkAllReadByContact 标记已读后，跨两轮未读总数清零
	if err := markAllRead.Handle(ctx, userSub, f.widget.ID, ""); err != nil {
		t.Fatal(err)
	}
	afterRead, err := stream.Handle(ctx, userSub, f.widget.ID, "", "", 10)
	if err != nil || afterRead.Unread != 0 {
		t.Fatalf("标记已读后未读数应为 0：unread=%d err=%v", afterRead.Unread, err)
	}

	// 5. 活跃会话中触发正在输入：应广播到当前活跃会话频道
	typingSub := hub.Subscribe(conversation.Channel(f.system.Tenant, s2.ConversationID))
	defer typingSub.Close()
	if err := typingActive.Handle(ctx, userSub, f.widget.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-typingSub.C():
		var ev conversation.Typing
		if err := json.Unmarshal(msg.Data, &ev); err != nil || ev.Type != "typing" || ev.Actor.ID != sess.Contact.ID {
			t.Fatalf("正在输入广播内容异常：%s %v", string(msg.Data), err)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到正在输入广播")
	}

	// 6. 权限与隔离：非联系人主体返回 ErrForbidden；另一个访客线索看不到用户消息
	if _, err := stream.Handle(ctx, f.agent, f.widget.ID, "", "", 10); !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("非联系人主体读取连续时间线应返回 ErrForbidden，实际 %v", err)
	}
	if err := markAllRead.Handle(ctx, f.agent, f.widget.ID, ""); !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("非联系人主体标记已读应返回 ErrForbidden，实际 %v", err)
	}
	if err := typingActive.Handle(ctx, f.agent, f.widget.ID); !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("非联系人主体触发正在输入应返回 ErrForbidden，实际 %v", err)
	}
	_, otherVisitor := f.session(t, SessionBody{})
	otherTimeline, err := stream.Handle(ctx, otherVisitor, f.widget.ID, "", "", 10)
	if err != nil || len(otherTimeline.Items) != 0 || otherTimeline.LatestConversation != nil {
		t.Fatalf("无关访客不应看到他人消息：%+v err=%v", otherTimeline, err)
	}
}
