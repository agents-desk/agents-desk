package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/push"
)

// MarkReadByContact 是联系人读到了成员发来的消息：消息的送达状态改为已读，成员在时间线中看到“已读”。
type MarkReadByContact struct {
	db   *bun.DB
	down Downstream
}

// NewMarkReadByContact 返回联系人标记已读的动作；down 的 Hub 把送达状态的变化推送给能看到会话的成员。
func NewMarkReadByContact(db *bun.DB, down Downstream) *MarkReadByContact {
	return &MarkReadByContact{db: db, down: down}
}

// Handle 把联系人 sub 在渠道 channelID 中的会话 id 里序号不大于 seq、发给他的消息记为已读。有消息变为已读时，
// 提交后推送给能看到会话的成员；已经都读过时什么也不做。失败的消息保持失败。
// sub 不是联系人时返回 ErrForbidden；会话不存在、属于别人或别的渠道时返回 ErrNotFound。
func (a *MarkReadByContact) Handle(ctx context.Context, sub identity.Subject, channelID, id string, seq int64) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findForContact(ctx, tx, sub, channelID, id, true)
		if err != nil {
			return err
		}
		res, err := tx.NewRaw(`UPDATE message_deliveries d SET status = ?, updated_at = ? FROM timeline_items t
			WHERE d.item_id = t.id AND t.conversation_id = ? AND t.seq <= ? AND t.actor_type <> ? AND d.status IN (?)`,
			Read, now(), c.ID, seq, ContactActor, bun.In([]DeliveryStatus{Sending, Delivered})).Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "mark messages read")
		}
		n, err := res.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "count messages marked read")
		}
		if n == 0 || a.down.Hub == nil {
			return nil
		}
		audience, err := audienceOf(ctx, tx, a.down.Roles, c, nil)
		if err != nil {
			return err
		}
		hub, tenant, ev := a.down.Hub, c.TenantID, push.Event{Type: push.Conversation, ID: c.ID, Seq: c.LastSeq}
		tx.AfterCommit(func(ctx context.Context) error {
			return errors.Wrap(push.Send(ctx, hub, tenant, audience, ev), "push messages read")
		})
		return nil
	})
}
