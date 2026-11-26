package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/push"
)

// ErrNotFailed 是要重发的消息没有发送失败：还在发送中，或者已经送达。
var ErrNotFailed = errorx.Define(errorx.Conflict, "conversation.not_failed")

// ItemInput 是指定会话中一项的请求：路径中的会话 ID 和项的 ID。
type ItemInput struct {
	ID     string `path:"id" format:"uuid"`
	ItemID string `path:"item_id" format:"uuid"`
}

// Retry 重发发送失败的消息：送达状态回到发送中，再交给连接器发出。
type Retry struct {
	db   *bun.DB
	down Downstream
}

// NewRetry 返回重发消息的动作；down 中的 DeliverTopic 发出消息，Hub 把送达状态的变化推送给能看到会话的成员。
func NewRetry(db *bun.DB, down Downstream) *Retry { return &Retry{db: db, down: down} }

// Handle 由 sub 重发他所在工作区中会话 id 里发送失败的消息 itemID：送达状态改为发送中，在同一个事务中发布发出消息的任务，
// 提交后推送给能看到会话的成员。会话不存在或 sub 看不到、消息不在这个会话中时返回 ErrNotFound；消息没有发送失败时返回
// ErrNotFailed；没有回复的权限时返回 ErrForbidden。
func (a *Retry) Handle(ctx context.Context, sub identity.Subject, id, itemID string) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		err = tx.NewSelect().Model(&item).Where("id = ? AND conversation_id = ?", itemID, c.ID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "load timeline item")
		}
		res, err := tx.NewUpdate().Model((*Delivery)(nil)).Set("status = ?", Sending).Set("error = ''").Set("updated_at = ?", now()).
			Where("item_id = ? AND status = ?", item.ID, Failed).Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "reset delivery")
		}
		n, err := res.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "count reset deliveries")
		}
		if n == 0 {
			return ErrNotFailed.New()
		}
		if err := DeliverTopic.Publish(ctx, tx, Appended{TenantID: c.TenantID, ConversationID: c.ID, ItemID: item.ID, Seq: item.Seq}); err != nil {
			return err
		}
		if a.down.Hub == nil {
			return nil
		}
		audience, err := audienceOf(ctx, tx, a.down.Roles, c, nil)
		if err != nil {
			return err
		}
		hub, tenant, ev := a.down.Hub, c.TenantID, push.Event{Type: pushType(c.Kind), ID: c.ID, Seq: c.LastSeq}
		tx.AfterCommit(func(ctx context.Context) error {
			return errors.Wrap(push.Send(ctx, hub, tenant, audience, ev), "push delivery retried")
		})
		return nil
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

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/items/{item_id}/retry。
func (a *Retry) HTTP(ctx context.Context, in *ItemInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.ItemID)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}
