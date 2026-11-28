package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/push"
	"agents-desk/internal/tickettype"
)

// PublishTicketUpdateBody 是向客户可见工单发布公开进度的请求体。
type PublishTicketUpdateBody struct {
	Text        string            `json:"text,omitempty" maxLength:"10000" doc:"公开进度文字内容"`
	Attachments []AttachmentInput `json:"attachments,omitempty" maxItems:"10" doc:"通过 /files 上传的媒体附件列表"`
	Translation *Translation      `json:"translation,omitempty" doc:"成员在发送前校对或微调过的某一种目标语言译文；向该语言的受影响客户同步时直接使用该译文"`
}

// PublishTicketUpdateInput 是发布公开进度的 HTTP 请求。
type PublishTicketUpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body PublishTicketUpdateBody
}

// PublishTicketUpdate 在客户可见的工单上发布一条公开进度消息（visibility = all），
// 写入工单时间线并经 Downstream 下发任务（TK3 将据此按客户语言去重翻译并同步到关联服务型会话）。
type PublishTicketUpdate struct {
	db   *bun.DB
	down Downstream
}

// NewPublishTicketUpdate 返回发布工单公开进度的动作。
func NewPublishTicketUpdate(db *bun.DB, down Downstream) *PublishTicketUpdate {
	return &PublishTicketUpdate{db: db, down: down}
}

// Handle 在 sub 可见的工单 id 上发布公开进度消息；若工单为 members（仅成员可见）则返回 ErrPrivateTicket。
func (a *PublishTicketUpdate) Handle(ctx context.Context, sub identity.Subject, id string, body PublishTicketUpdateBody) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	if err := requireMessageContent(body.Text, len(body.Attachments)); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, tk, err := findVisibleTicket(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if tk.Visibility != tickettype.VisibilityAll {
			return ErrPrivateTicket.New()
		}
		confirmed, err := confirmAttachmentInputs(ctx, tx, a.down.Media.Files, sub, body.Attachments)
		if err != nil {
			return err
		}
		actor := actorOf(sub)
		item = Item{
			Kind:       Message,
			ActorType:  actor,
			ActorID:    sub.ID,
			Visibility: Everyone,
			Content:    Content{Text: body.Text},
		}
		if err := applyVerifiedTranslation(ctx, tx, sub, &item, body.Translation); err != nil {
			return err
		}
		c.LastMessageAt = database.Now()
		if err := join(ctx, tx, c.ID, actor, sub.ID); err != nil {
			return err
		}
		if err := commit(ctx, tx, a.down, c, &item); err != nil {
			return err
		}
		return insertAttachments(ctx, tx, c.TenantID, item.ID, confirmed)
	})
	if err != nil {
		return nil, err
	}
	dtos, err := toItemDTOs(ctx, a.db, a.down.Media, []Item{item})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 POST /t/{slug}/api/tickets/{id}/updates。
func (a *PublishTicketUpdate) HTTP(ctx context.Context, in *PublishTicketUpdateInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}

// SaveTicketBroadcastCount 将工单公开进度实际同步到的关联客户数写入该时间线项的 derived.broadcast_count，
// 并向能看到该工单的成员推送 ItemSeq 刷新事件。
func SaveTicketBroadcastCount(ctx context.Context, db *bun.DB, down Downstream, tenantID, itemID string, count int) error {
	return database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		var it Item
		err := tx.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", itemID, tenantID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "load ticket update item for broadcast count")
		}
		if it.Derived == nil {
			it.Derived = map[string]any{}
		}
		if prev, ok := it.Derived["broadcast_count"].(float64); ok && int(prev) == count {
			return nil
		}
		it.Derived["broadcast_count"] = count
		if _, err := tx.NewUpdate().Model(&it).Column("derived").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save ticket broadcast count")
		}
		if down.Hub == nil {
			return nil
		}
		c, err := find(ctx, tx, tenantID, it.ConversationID, false)
		if err != nil {
			return err
		}
		audience, err := audienceOf(ctx, tx, down.Roles, c, nil)
		if err != nil || len(audience) == 0 {
			return err
		}
		hub, ev := down.Hub, push.Event{Type: push.Ticket, ID: c.ID, Seq: c.LastSeq, ItemSeq: it.Seq}
		tx.AfterCommit(func(ctx context.Context) error {
			return errors.Wrap(push.Send(ctx, hub, tenantID, audience, ev), "push ticket broadcast count")
		})
		return nil
	})
}
