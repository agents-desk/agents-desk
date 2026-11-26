package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// LinkConversationInput 是把一条服务型会话关联到已有工单的 HTTP 请求。
type LinkConversationInput struct {
	ID   string `path:"id" format:"uuid" doc:"工单 ID"`
	Body struct {
		ConversationID string `json:"conversation_id" format:"uuid" doc:"要关联的服务型会话 ID"`
	}
}

// LinkConversation 把一条可见的服务型会话关联到已有工单，支持多个同类问题会话归集到同一张工单。
type LinkConversation struct {
	db   *bun.DB
	down Downstream
}

// NewLinkConversation 返回关联服务型会话到工单的动作。
func NewLinkConversation(db *bun.DB, down Downstream) *LinkConversation {
	return &LinkConversation{db: db, down: down}
}

// Handle 将服务型会话 conversationID 关联到工单 ticketID，并在两端时间线记录系统事件；
// 任一侧不存在或对 sub 不可见返回 ErrNotFound；已关联返回 ErrLinkAlreadyExists。
func (a *LinkConversation) Handle(ctx context.Context, sub identity.Subject, ticketID, conversationID string) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		// 按确定顺序锁两行 conversations，避免并发双向加锁死锁
		first, second := ticketID, conversationID
		if first > second {
			first, second = second, first
		}
		if _, err := findVisible(ctx, tx, sub, first, true); err != nil {
			return err
		}
		if first != second {
			if _, err := findVisible(ctx, tx, sub, second, true); err != nil {
				return err
			}
		}

		tc, tk, err := findVisibleTicket(ctx, tx, sub, ticketID, true)
		if err != nil {
			return err
		}
		sc, err := findVisible(ctx, tx, sub, conversationID, true)
		if err != nil {
			return err
		}
		if sc.Kind != Service {
			return ErrWrongKind.New()
		}

		var createdBy *string
		if sub.Type == member.Type {
			id := sub.ID
			createdBy = &id
		}
		link := &ConversationLink{
			ID:             newID(),
			TenantID:       sub.Tenant,
			TicketID:       tc.ID,
			ConversationID: sc.ID,
			Relation:       LinkLinked,
			CreatedBy:      createdBy,
		}
		if _, err := tx.NewInsert().Model(link).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "conversation_links_unique") {
				return ErrLinkAlreadyExists.New()
			}
			return errors.Wrap(err, "insert conversation link")
		}

		tc.LastMessageAt = now()
		tcEv := event(sub, "ticket.conversation_linked", map[string]any{"conversation_id": sc.ID})
		if err := commit(ctx, tx, a.down, tc, tcEv); err != nil {
			return err
		}
		scEv := event(sub, "conversation.ticket_linked", map[string]any{
			"ticket_id": tc.ID,
			"number":    tk.Number,
			"title":     tk.Title,
		})
		return commit(ctx, tx, a.down, sc, scEv)
	})
	if err != nil {
		return nil, err
	}
	return NewGetTicket(a.db).Handle(ctx, sub, ticketID)
}

// HTTP 处理 POST /t/{slug}/api/tickets/{id}/links。
func (a *LinkConversation) HTTP(ctx context.Context, in *LinkConversationInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.ConversationID)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}

// UnlinkConversationInput 是取消工单与服务型会话关联的 HTTP 请求。
type UnlinkConversationInput struct {
	ID             string `path:"id" format:"uuid" doc:"工单 ID"`
	ConversationID string `path:"conversation_id" format:"uuid" doc:"要取消关联的服务型会话 ID"`
}

// UnlinkConversation 取消工单与某条服务型会话的关联。
type UnlinkConversation struct {
	db   *bun.DB
	down Downstream
}

// NewUnlinkConversation 返回取消关联服务型会话的动作。
func NewUnlinkConversation(db *bun.DB, down Downstream) *UnlinkConversation {
	return &UnlinkConversation{db: db, down: down}
}

// Handle 取消工单 ticketID 与服务型会话 conversationID 的关联，并在两端时间线写入系统事件。
// 要求 sub 同时可见该工单与该会话；关联不存在返回 ErrLinkNotFound。
func (a *UnlinkConversation) Handle(ctx context.Context, sub identity.Subject, ticketID, conversationID string) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		first, second := ticketID, conversationID
		if first > second {
			first, second = second, first
		}
		if _, err := findVisible(ctx, tx, sub, first, true); err != nil {
			return err
		}
		if first != second {
			if _, err := findVisible(ctx, tx, sub, second, true); err != nil {
				return err
			}
		}

		tc, tk, err := findVisibleTicket(ctx, tx, sub, ticketID, true)
		if err != nil {
			return err
		}
		sc, err := findVisible(ctx, tx, sub, conversationID, true)
		if err != nil {
			return err
		}
		if sc.Kind != Service {
			return ErrWrongKind.New()
		}

		res, err := tx.NewDelete().Model((*ConversationLink)(nil)).
			Where("tenant_id = ? AND ticket_id = ? AND conversation_id = ?", sub.Tenant, tc.ID, sc.ID).
			Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "delete conversation link")
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "delete conversation link rows affected")
		}
		if affected == 0 {
			return ErrLinkNotFound.New()
		}

		tc.LastMessageAt = now()
		tcEv := event(sub, "ticket.conversation_unlinked", map[string]any{"conversation_id": sc.ID})
		if err := commit(ctx, tx, a.down, tc, tcEv); err != nil {
			return err
		}
		scEv := event(sub, "conversation.ticket_unlinked", map[string]any{
			"ticket_id": tc.ID,
			"number":    tk.Number,
			"title":     tk.Title,
		})
		return commit(ctx, tx, a.down, sc, scEv)
	})
	if err != nil {
		return nil, err
	}
	return NewGetTicket(a.db).Handle(ctx, sub, ticketID)
}

// HTTP 处理 DELETE /t/{slug}/api/tickets/{id}/links/{conversation_id}。
func (a *UnlinkConversation) HTTP(ctx context.Context, in *UnlinkConversationInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.ConversationID)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}
