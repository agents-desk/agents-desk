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

// RetryBody 是重发消息的可选参数，用于决定是否跳过自动翻译直接按原文发出。
type RetryBody struct {
	SkipTranslation bool `json:"skip_translation,omitempty" doc:"为 true 时清除待自动翻译标记并直接按原文重新投递"`
}

// RetryInput 是指定会话中一项的重发请求：路径中的会话 ID、项 ID 与可选的重发策略。
type RetryInput struct {
	ID     string `path:"id" format:"uuid"`
	ItemID string `path:"item_id" format:"uuid"`
	Body   *RetryBody
}

// Retry 重发发送失败的消息：送达状态回到发送中，再交给连接器发出。
type Retry struct {
	db   *bun.DB
	down Downstream
}

// NewRetry 返回重发消息的动作；down 中的 DeliverTopic 发出消息，Hub 把送达状态的变化推送给能看到会话的成员。
func NewRetry(db *bun.DB, down Downstream) *Retry { return &Retry{db: db, down: down} }

// Handle 由 sub 重发他所在工作区中会话 id 里发送失败的消息 itemID：送达状态改为发送中，
// 当 opts[0].SkipTranslation 为 true 时清除待自动翻译标记按原文发送，否则保留待翻译标记重试翻译并投递。
// 在同一个事务中发布发出消息的任务，提交后推送给能看到会话的成员。
func (a *Retry) Handle(ctx context.Context, sub identity.Subject, id, itemID string, opts ...RetryBody) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	var opt RetryBody
	if len(opts) > 0 {
		opt = opts[0]
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		err = tx.NewSelect().Model(&item).Where("id = ? AND conversation_id = ?", itemID, c.ID).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "load timeline item")
		}
		if item.Derived != nil {
			derivedChanged := false
			if _, hasFailed := item.Derived["translation_failed"]; hasFailed {
				delete(item.Derived, "translation_failed")
				derivedChanged = true
			}
			if opt.SkipTranslation {
				if _, hasPending := item.Derived["pending_translation"]; hasPending {
					delete(item.Derived, "pending_translation")
					derivedChanged = true
				}
			}
			if derivedChanged {
				if _, err := tx.NewUpdate().Model((*Item)(nil)).Set("derived = ?", item.Derived).
					Where("id = ? AND tenant_id = ?", item.ID, c.TenantID).Exec(ctx); err != nil {
					return errors.Wrap(err, "update retry derived flags")
				}
			}
		}
		res, err := tx.NewUpdate().Model((*Delivery)(nil)).Set("status = ?", Sending).Set("error = ''").Set("updated_at = ?", database.Now()).
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
func (a *Retry) HTTP(ctx context.Context, in *RetryInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	var opt RetryBody
	if in.Body != nil {
		opt = *in.Body
	}
	item, err := a.Handle(ctx, sub, in.ID, in.ItemID, opt)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}
