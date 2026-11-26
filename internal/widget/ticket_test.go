package widget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/push"
	"agents-desk/internal/tickettype"
)

// TestContactTicketsVisibilityAndCrossChannel 验证客户侧工单接口的可见性隔离、跨渠道关联与实时推送：
//   - 已登录用户（user）以 contact_id 为主体跨渠道可见全部关联的 visibility=all 工单，不写死当前插件渠道；
//   - 客户详情仅返回 visibility=all 的公开进度，绝不泄露内部备注；
//   - visibility=members 内部工单与其他联系人的工单均不可见；
//   - 访客线索（lead）仅可见当前插件渠道关联的工单，且非联系人主体访问被拒绝；
//   - 工单发布公开进度与改状态会向受影响客户的网页聊天插件频道推送 push.Ticket 事件。
func TestContactTicketsVisibilityAndCrossChannel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "tkwidget")

	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	var pushed []struct {
		ch, contact string
		ev          push.Event
	}
	down := conversation.Downstream{
		ContactPush: func(_ context.Context, chID, contactID string, ev push.Event) error {
			pushed = append(pushed, struct {
				ch, contact string
				ev          push.Event
			}{ch: chID, contact: contactID, ev: ev})
			return Push(hub)(ctx, chID, contactID, ev)
		},
	}

	tt, err := tickettype.NewCreate(env.DB).Handle(ctx, f.agent, tickettype.CreateParams{
		Name:              "充值问题",
		DefaultVisibility: tickettype.VisibilityAll,
	})
	if err != nil {
		t.Fatalf("create ticket type: %v", err)
	}

	// 已登录用户在网页聊天插件建立身份
	inst, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com").Handle(ctx, f.agent, f.widget.ID)
	if err != nil {
		t.Fatalf("get installation: %v", err)
	}
	tok := sign(t, inst.IdentitySecret, userClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user-rahul-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		Name:             "Rahul",
		Email:            "rahul@example.com",
	}, jwt.SigningMethodHS256)
	sess, userSub := f.session(t, SessionBody{UserToken: tok})

	// 该用户通过另一个外部渠道（例如 Telegram）发起服务型会话
	otherCh := &channel.Channel{
		ID:       newID(),
		TenantID: f.system.Tenant,
		Kind:     channel.Test,
		Name:     "Telegram 渠道",
		Enabled:  true,
		Settings: map[string]any{},
	}
	if _, err := env.DB.NewInsert().Model(otherCh).Exec(ctx); err != nil {
		t.Fatalf("insert other channel: %v", err)
	}
	if _, err := env.DB.NewInsert().Model(&contact.Identity{
		TenantID:   f.system.Tenant,
		ContactID:  sess.Contact.ID,
		ChannelID:  otherCh.ID,
		ExternalID: "tg-rahul",
		CreatedAt:  time.Now().UTC(),
	}).Exec(ctx); err != nil {
		t.Fatalf("insert external identity: %v", err)
	}
	registry := channel.Registry{channel.Test: NewConnector(hub), channel.Widget: NewConnector(hub)}
	receive := conversation.NewReceive(env.DB, registry, down)
	otherRecv, err := receive.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: otherCh.ID,
		From:      "tg-rahul",
		Text:      "UPI payment pending on Telegram",
	})
	if err != nil {
		t.Fatalf("receive other channel msg: %v", err)
	}

	// 由该跨渠道会话创建客户可见工单，写入内部备注 + 公开进度 + 改状态为等待客户
	createTicket := conversation.NewCreateTicket(env.DB, down)
	publicTk, err := createTicket.Handle(ctx, f.agent, conversation.CreateTicketParams{
		TypeID:               tt.ID,
		Title:                "UPI 支付延迟排查",
		Description:          "正在核对银行流水",
		Visibility:           tickettype.VisibilityAll,
		SourceConversationID: &otherRecv.ConversationID,
	})
	if err != nil {
		t.Fatalf("create public ticket: %v", err)
	}
	if _, err := conversation.NewAddNote(env.DB, down).Handle(ctx, f.agent, publicTk.ID, "内部讨论：不要发给客户", nil); err != nil {
		t.Fatalf("add note: %v", err)
	}
	if _, err := conversation.NewPublishTicketUpdate(env.DB, down).Handle(ctx, f.agent, publicTk.ID, conversation.PublishTicketUpdateBody{
		Text: "已提交支付通道补单，预计 10 分钟到账。",
	}); err != nil {
		t.Fatalf("publish update: %v", err)
	}
	if _, err := conversation.NewSetTicketStatus(env.DB, down).Handle(ctx, f.agent, publicTk.ID, conversation.TicketWaiting); err != nil {
		t.Fatalf("set status waiting: %v", err)
	}

	// 另建一张仅成员可见的内部工单关联同一会话
	privateTk, err := createTicket.Handle(ctx, f.agent, conversation.CreateTicketParams{
		TypeID:               tt.ID,
		Title:                "内部风控复核",
		Visibility:           tickettype.VisibilityMembers,
		SourceConversationID: &otherRecv.ConversationID,
	})
	if err != nil {
		t.Fatalf("create private ticket: %v", err)
	}

	// 验证实时推送收到了 push.Ticket 到该用户的网页插件频道
	gotTicketPush := false
	for _, p := range pushed {
		if p.ch == f.widget.ID && p.contact == sess.Contact.ID && p.ev.Type == push.Ticket && p.ev.ID == publicTk.ID {
			gotTicketPush = true
			break
		}
	}
	if !gotTicketPush {
		t.Fatalf("expected push.Ticket to widget channel %s for contact %s, got %+v", f.widget.ID, sess.Contact.ID, pushed)
	}

	// 验证客户侧列表只返回公开工单、返回跨渠道已有会话 ID 与最新公开进度
	listAct := conversation.NewListTicketsForContact(env.DB)
	getAct := conversation.NewGetTicketForContact(env.DB, conversation.Media{})

	list, err := listAct.Handle(ctx, userSub, f.widget.ID)
	if err != nil {
		t.Fatalf("list contact tickets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 visible ticket, got %d", len(list))
	}
	if list[0].ID != publicTk.ID || list[0].Status != conversation.TicketWaiting {
		t.Fatalf("unexpected summary: %+v", list[0])
	}
	if list[0].LatestUpdate != "已提交支付通道补单，预计 10 分钟到账。" {
		t.Fatalf("unexpected latest update: %q", list[0].LatestUpdate)
	}

	// 验证工单详情仅包含公开进度，不含内部备注
	detail, err := getAct.Handle(ctx, userSub, f.widget.ID, publicTk.ID)
	if err != nil {
		t.Fatalf("get contact ticket: %v", err)
	}
	if len(detail.Updates) != 1 || detail.Updates[0].Text != "已提交支付通道补单，预计 10 分钟到账。" {
		t.Fatalf("want 1 public update without internal notes, got %+v", detail.Updates)
	}

	// 仅成员可见的工单对客户返回 ErrNotFound
	if _, err := getAct.Handle(ctx, userSub, f.widget.ID, privateTk.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("want ErrNotFound for private ticket, got %v", err)
	}

	// 另一位访客看不到任何工单
	_, otherLead := f.session(t, SessionBody{})
	otherList, err := listAct.Handle(ctx, otherLead, f.widget.ID)
	if err != nil {
		t.Fatalf("list other lead tickets: %v", err)
	}
	if len(otherList) != 0 {
		t.Fatalf("want 0 tickets for other lead, got %d", len(otherList))
	}
	if _, err := getAct.Handle(ctx, otherLead, f.widget.ID, publicTk.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("want ErrNotFound for other lead, got %v", err)
	}

	// 成员主体调用客户工单接口返回 ErrForbidden
	if _, err := listAct.Handle(ctx, f.agent, f.widget.ID); !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("want ErrForbidden for member subject, got %v", err)
	}
}
