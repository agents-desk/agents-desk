package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/push"
)

// ErrMessageRecallWindow 是超过了可以撤回已发出消息的时间。
var ErrMessageRecallWindow = errorx.Define(errorx.Conflict, "conversation.message_recall_window")

// recallWindow 是可以撤回已经发出的消息的时间范围；内部备注不受限。
const recallWindow = 24 * time.Hour

// RecallMessageInput 是撤回消息的 HTTP 请求。
type RecallMessageInput struct {
	ID     string `path:"id" format:"uuid"`
	ItemID string `path:"item_id" format:"uuid"`
}

// RecallMessage 撤回自己发出的一条消息或内部备注：时间线项保持原样，撤回作为一条独立的记录追加。
// 只有不对客户发出的内容可以撤回（内部聊天的消息和内部备注），发出的消息请发送更正。
type RecallMessage struct {
	db   *bun.DB
	down Downstream
}

// NewRecallMessage 返回撤回消息的动作；down 用于提交后通知能看到会话的成员。
func NewRecallMessage(db *bun.DB, down Downstream) *RecallMessage {
	return &RecallMessage{db: db, down: down}
}

// Handle 由成员 sub 撤回会话 conversationID 中自己发出的消息 itemID。重复撤回幂等。
// 不是作者返回 ErrMessageNotAuthor；不能撤回返回 ErrMessageNotEditable；超过时间返回 ErrMessageRecallWindow。
func (a *RecallMessage) Handle(ctx context.Context, sub identity.Subject, conversationID, itemID string) error {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return err
	}
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, conversationID, true)
		if err != nil {
			return err
		}
		var item Item
		if err := tx.NewSelect().Model(&item).
			Where("id = ? AND tenant_id = ? AND conversation_id = ?", itemID, c.TenantID, c.ID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return errors.Wrap(err, "load item for recall")
		}
		if item.ActorType != MemberActor || item.ActorID != sub.ID {
			return ErrMessageNotAuthor.New()
		}
		if item.Kind != Message && item.Kind != Note {
			return ErrMessageNotEditable.New()
		}
		switch c.Kind {
		case Chat:
			var scope ChatScope
			if err := tx.NewSelect().Table("chats").Column("scope").
				Where("conversation_id = ?", c.ID).Scan(ctx, &scope); err != nil {
				return errors.Wrap(err, "load chat scope for recall")
			}
			if scope != ChatInternal && item.Kind != Note {
				return ErrMessageNotEditable.New()
			}
		case Service, Ticket:
			if item.Kind != Note {
				return ErrMessageNotEditable.New()
			}
		default:
			return ErrMessageNotEditable.New()
		}
		if item.Kind == Message && now().Sub(item.CreatedAt) > recallWindow {
			return ErrMessageRecallWindow.New()
		}
		if _, err := tx.NewRaw(`INSERT INTO message_recalls (item_id, tenant_id, conversation_id, recalled_by, created_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			item.ID, c.TenantID, c.ID, sub.ID, now()).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert message recall")
		}
		// 撤回后从全文索引中移除
		if err := SearchIndex.Sync(ctx, tx, item.ID); err != nil {
			return err
		}
		if a.down.Hub != nil {
			audience, err := audienceOf(ctx, tx, a.down.Roles, c, nil)
			if err != nil {
				return err
			}
			if len(audience) > 0 {
				hub, tenant, ev := a.down.Hub, c.TenantID, push.Event{
					Type: pushType(c.Kind), ID: c.ID, Seq: c.LastSeq, ItemSeq: item.Seq,
				}
				tx.AfterCommit(func(ctx context.Context) error {
					return errors.Wrap(push.Send(ctx, hub, tenant, audience, ev), "push message recalled")
				})
			}
		}
		return nil
	})
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/items/{item_id}/recall。
func (a *RecallMessage) HTTP(ctx context.Context, in *RecallMessageInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID, in.ItemID)
}
