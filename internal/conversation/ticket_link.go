package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
	"agents-desk/internal/member"
)

// LinkTicketContactBody 是关联受影响联系人或外部群到已有工单的请求体：传 contact_id、external_group_id 或 conversation_id（自动取该会话对象）。
type LinkTicketContactBody struct {
	ContactID       *string `json:"contact_id,omitempty" format:"uuid" doc:"要关联的受影响联系人 ID"`
	ExternalGroupID *string `json:"external_group_id,omitempty" format:"uuid" doc:"要关联的受影响外部群 ID"`
	ConversationID  *string `json:"conversation_id,omitempty" format:"uuid" doc:"可选：按服务型会话 ID 自动关联其联系人或外部群"`
}

// LinkTicketContactInput 是关联受影响客户到工单的 HTTP 请求。
type LinkTicketContactInput struct {
	ID   string `path:"id" format:"uuid" doc:"工单 ID"`
	Body LinkTicketContactBody
}

// LinkTicketContact 把一位受影响客户关联到已有工单，支持将多个同类问题客户归集到同一张工单统一协作和广播进度。
type LinkTicketContact struct {
	db   *bun.DB
	down Downstream
}

// NewLinkTicketContact 返回关联受影响客户到工单的动作。
func NewLinkTicketContact(db *bun.DB, down Downstream) *LinkTicketContact {
	return &LinkTicketContact{db: db, down: down}
}

// Handle 将联系人、外部群或服务型会话对应的对象关联到工单 ticketID，并在工单及对应会话时间线记录系统事件；
// 任一侧不存在或对 sub 不可见返回 ErrNotFound / contact.ErrNotFound / extgroup.ErrNotFound；已关联返回 ErrLinkAlreadyExists。
func (a *LinkTicketContact) Handle(ctx context.Context, sub identity.Subject, ticketID string, body LinkTicketContactBody) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	hasContact := body.ContactID != nil && *body.ContactID != ""
	hasGroup := body.ExternalGroupID != nil && *body.ExternalGroupID != ""
	hasConv := body.ConversationID != nil && *body.ConversationID != ""
	if !hasContact && !hasGroup && !hasConv {
		return nil, contact.ErrNotFound.New()
	}

	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var (
			tc              *Conversation
			tk              *TicketMeta
			sc              *Conversation
			contactID       string
			externalGroupID string
		)
		if hasConv {
			// 按确定顺序锁两行 conversations，避免并发双向加锁死锁
			convID := *body.ConversationID
			first, second := ticketID, convID
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
			var err error
			tc, tk, err = findVisibleTicket(ctx, tx, sub, ticketID, true)
			if err != nil {
				return err
			}
			sc, err = findVisible(ctx, tx, sub, convID, true)
			if err != nil {
				return err
			}
			if sc.Kind != Service {
				return ErrWrongKind.New()
			}
			switch {
			case sc.ContactID != nil && *sc.ContactID != "":
				contactID = *sc.ContactID
			case sc.ExternalGroupID != nil && *sc.ExternalGroupID != "":
				externalGroupID = *sc.ExternalGroupID
			default:
				return contact.ErrNotFound.New()
			}
		} else if hasGroup {
			var err error
			tc, tk, err = findVisibleTicket(ctx, tx, sub, ticketID, true)
			if err != nil {
				return err
			}
			externalGroupID = *body.ExternalGroupID
			if err := ensureVisibleGroup(ctx, tx, sub, externalGroupID); err != nil {
				return err
			}
			active := new(Conversation)
			q := tx.NewSelect().Model(active).
				Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.external_group_id = ? AND conversation.status IN (?)",
					sub.Tenant, Service, externalGroupID, bun.In([]Status{Open, Snoozed})).
				OrderExpr("conversation.last_message_at DESC, conversation.id DESC").Limit(1)
			q = visible(q, sub)
			if err := q.For("UPDATE OF conversation").Scan(ctx); err == nil {
				sc = active
			} else if !errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(err, "find active conversation for group ticket link")
			}
		} else {
			var err error
			tc, tk, err = findVisibleTicket(ctx, tx, sub, ticketID, true)
			if err != nil {
				return err
			}
			contactID = *body.ContactID
			if err := ensureVisibleContact(ctx, tx, sub, contactID); err != nil {
				return err
			}
			// 若该联系人当前有可见的活跃服务型会话，同步在会话中留痕
			active := new(Conversation)
			q := tx.NewSelect().Model(active).
				Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.contact_id = ? AND conversation.status IN (?)",
					sub.Tenant, Service, contactID, bun.In([]Status{Open, Snoozed})).
				OrderExpr("conversation.last_message_at DESC, conversation.id DESC").Limit(1)
			q = visible(q, sub)
			if err := q.For("UPDATE OF conversation").Scan(ctx); err == nil {
				sc = active
			} else if !errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(err, "find active conversation for contact ticket link")
			}
		}

		var createdBy *string
		if sub.Type == member.Type {
			id := sub.ID
			createdBy = &id
		}
		link := &TicketTarget{
			ID:        newID(),
			TenantID:  sub.Tenant,
			TicketID:  tc.ID,
			CreatedBy: createdBy,
		}
		var evData map[string]any
		if contactID != "" {
			cid := contactID
			link.ContactID = &cid
			var contactName string
			if err := tx.NewSelect().Table("contacts").Column("name").Where("id = ? AND tenant_id = ?", contactID, sub.Tenant).Scan(ctx, &contactName); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(err, "load contact name for link event")
			}
			evData = map[string]any{"contact_id": contactID, "contact_name": contactName}
		} else {
			gid := externalGroupID
			link.ExternalGroupID = &gid
			var groupName string
			if err := tx.NewSelect().Table("external_groups").Column("name").Where("id = ? AND tenant_id = ?", externalGroupID, sub.Tenant).Scan(ctx, &groupName); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(err, "load external group name for link event")
			}
			evData = map[string]any{"external_group_id": externalGroupID, "group_name": groupName}
		}
		if _, err := tx.NewInsert().Model(link).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "ticket_targets_ticket_contact_unique") || database.IsUniqueViolation(err, "ticket_targets_ticket_group_unique") {
				return ErrLinkAlreadyExists.New()
			}
			return errors.Wrap(err, "insert ticket target")
		}

		tc.LastMessageAt = now()
		tcEv := event(sub, "ticket.contact_linked", evData)
		if err := commit(ctx, tx, a.down, tc, tcEv); err != nil {
			return err
		}
		if sc != nil {
			scEv := event(sub, "conversation.ticket_linked", map[string]any{
				"ticket_id": tc.ID,
				"number":    tk.Number,
				"title":     tk.Title,
			})
			return commit(ctx, tx, a.down, sc, scEv)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewGetTicket(a.db).Handle(ctx, sub, ticketID)
}

// HTTP 处理 POST /t/{slug}/api/tickets/{id}/contacts。
func (a *LinkTicketContact) HTTP(ctx context.Context, in *LinkTicketContactInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}

// UnlinkTicketContactInput 是取消工单与受影响客户关联的 HTTP 请求。
type UnlinkTicketContactInput struct {
	ID        string `path:"id" format:"uuid" doc:"工单 ID"`
	ContactID string `path:"contact_id" format:"uuid" doc:"要取消关联的联系人 ID"`
}

// UnlinkTicketContact 取消工单与某位受影响客户的关联。
type UnlinkTicketContact struct {
	db   *bun.DB
	down Downstream
}

// NewUnlinkTicketContact 返回取消关联受影响客户的动作。
func NewUnlinkTicketContact(db *bun.DB, down Downstream) *UnlinkTicketContact {
	return &UnlinkTicketContact{db: db, down: down}
}

// Handle 取消工单 ticketID 与联系人 contactID 的关联，并在工单及该联系人活跃会话写入系统事件。
// 要求 sub 同时可见该工单与该联系人；关联不存在返回 ErrLinkNotFound。
func (a *UnlinkTicketContact) Handle(ctx context.Context, sub identity.Subject, ticketID, contactID string) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		tc, tk, err := findVisibleTicket(ctx, tx, sub, ticketID, true)
		if err != nil {
			return err
		}
		if err := ensureVisibleContact(ctx, tx, sub, contactID); err != nil {
			return err
		}

		res, err := tx.NewDelete().Model((*TicketTarget)(nil)).
			Where("tenant_id = ? AND ticket_id = ? AND contact_id = ?", sub.Tenant, tc.ID, contactID).
			Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "delete ticket contact")
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "delete ticket contact rows affected")
		}
		if affected == 0 {
			return ErrLinkNotFound.New()
		}

		var contactName string
		if err := tx.NewSelect().Table("contacts").Column("name").Where("id = ? AND tenant_id = ?", contactID, sub.Tenant).Scan(ctx, &contactName); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errors.Wrap(err, "load contact name for unlink event")
		}

		tc.LastMessageAt = now()
		tcEv := event(sub, "ticket.contact_unlinked", map[string]any{
			"contact_id":   contactID,
			"contact_name": contactName,
		})
		if err := commit(ctx, tx, a.down, tc, tcEv); err != nil {
			return err
		}

		// 若该联系人当前有活跃服务型会话，在会话时间线中留痕
		active := new(Conversation)
		q := tx.NewSelect().Model(active).
			Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.contact_id = ? AND conversation.status IN (?)",
				sub.Tenant, Service, contactID, bun.In([]Status{Open, Snoozed})).
			OrderExpr("conversation.last_message_at DESC, conversation.id DESC").Limit(1)
		q = visible(q, sub)
		if err := q.For("UPDATE OF conversation").Scan(ctx); err == nil {
			scEv := event(sub, "conversation.ticket_unlinked", map[string]any{
				"ticket_id": tc.ID,
				"number":    tk.Number,
				"title":     tk.Title,
			})
			return commit(ctx, tx, a.down, active, scEv)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return errors.Wrap(err, "find active conversation for contact ticket unlink")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewGetTicket(a.db).Handle(ctx, sub, ticketID)
}

// HTTP 处理 DELETE /t/{slug}/api/tickets/{id}/contacts/{contact_id}。
func (a *UnlinkTicketContact) HTTP(ctx context.Context, in *UnlinkTicketContactInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.ContactID)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}
