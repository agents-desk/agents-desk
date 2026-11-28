package conversation

import (
	"context"
	"database/sql"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
)

// MarkSpamBody 是标记垃圾会话的请求体。
type MarkSpamBody struct {
	BlockContact bool   `json:"block_contact,omitempty" doc:"是否同时拉黑会话所属联系人"`
	Reason       string `json:"reason,omitempty" maxLength:"500" doc:"同时拉黑联系人时的原因说明"`
}

// MarkSpamInput 是标记垃圾会话的 HTTP 请求。
type MarkSpamInput struct {
	ID   string `path:"id" format:"uuid"`
	Body MarkSpamBody
}

// MarkSpam 将服务型会话标记为垃圾会话并关闭它，从常规收件箱与统计报表中排除。
type MarkSpam struct {
	db   *bun.DB
	down Downstream
}

// NewMarkSpam 返回标记垃圾会话的动作；down 是时间线有新内容时通知的下游。
func NewMarkSpam(db *bun.DB, down Downstream) *MarkSpam {
	return &MarkSpam{db: db, down: down}
}

// Handle 在事务中将 sub 所在工作区中可见的服务型会话 id 标记为垃圾并关闭；
// 当 b.BlockContact 为真且存在关联联系人时，先于会话锁定联系人并同步拉黑及吊销令牌，遵守 contacts -> conversations 全局锁序。
func (a *MarkSpam) Handle(ctx context.Context, sub identity.Subject, id string, b MarkSpamBody) (*ConversationDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	if b.BlockContact {
		if err := authorize(sub, auth.ContactsUpdate); err != nil {
			return nil, err
		}
	}
	reason := strings.TrimSpace(b.Reason)
	var updated *Conversation
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var lockedContactID string
		if b.BlockContact {
			var meta struct {
				Kind      Kind    `bun:"kind"`
				ContactID *string `bun:"contact_id"`
			}
			err := tx.NewSelect().Table("conversations").Column("kind", "contact_id").
				Where("id = ? AND tenant_id = ?", id, sub.Tenant).
				Scan(ctx, &meta)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(err, "load conversation contact before spam")
			}
			if err == nil && meta.Kind == Service && meta.ContactID != nil && *meta.ContactID != "" {
				if err := contact.LockPair(ctx, tx, sub.Tenant, *meta.ContactID, ""); err != nil {
					return err
				}
				lockedContactID = *meta.ContactID
			}
		}

		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if c.Kind != Service {
			return ErrWrongKind.New()
		}

		at := now()
		blockedContact := false
		if lockedContactID != "" && c.ContactID != nil && *c.ContactID == lockedContactID {
			if _, err := tx.NewUpdate().Table("contacts").
				Set("blocked_at = ?", at).
				Set("blocked_reason = ?", reason).
				Set("updated_at = ?", at).
				Where("id = ? AND tenant_id = ?", lockedContactID, c.TenantID).
				Exec(ctx); err != nil {
				return errors.Wrap(err, "block conversation contact")
			}
			if a.down.RevokeContactTokens != nil {
				if err := a.down.RevokeContactTokens(ctx, tx, c.TenantID, lockedContactID); err != nil {
					return err
				}
			}
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event: "contact.blocked", TargetType: string(contact.Type), TargetID: lockedContactID,
				Properties: map[string]any{"reason": reason},
			}); err != nil {
				return err
			}
			blockedContact = true
		}

		if c.SpamAt != nil {
			updated = c
			return nil
		}

		var items []*Item
		if c.Status != Closed {
			closeItems, err := applyClose(ctx, tx, a.down, sub, c)
			if err != nil {
				return err
			}
			items = append(items, closeItems...)
		}
		c.SpamAt = &at
		items = append(items, event(sub, "conversation.marked_spam", map[string]any{
			"block_contact": blockedContact,
		}))
		if err := commit(ctx, tx, a.down, c, items...); err != nil {
			return err
		}
		updated = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	dtos, err := toDTOs(ctx, a.db, sub, []Conversation{*updated})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/spam。
func (a *MarkSpam) HTTP(ctx context.Context, in *MarkSpamInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}

// UnmarkSpam 取消会话的垃圾标记，将其从垃圾会话列表中移回已关闭历史。
type UnmarkSpam struct {
	db   *bun.DB
	down Downstream
}

// NewUnmarkSpam 返回取消垃圾会话标记的动作。
func NewUnmarkSpam(db *bun.DB, down Downstream) *UnmarkSpam {
	return &UnmarkSpam{db: db, down: down}
}

// Handle 清除 sub 所在工作区中可见服务型会话 id 的垃圾标记，并在时间线中追加 conversation.unmarked_spam。
func (a *UnmarkSpam) Handle(ctx context.Context, sub identity.Subject, id string) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, _ bun.IDB, c *Conversation) ([]*Item, error) {
		if c.Kind != Service {
			return nil, ErrWrongKind.New()
		}
		if c.SpamAt == nil {
			return nil, nil
		}
		c.SpamAt = nil
		return []*Item{event(sub, "conversation.unmarked_spam", nil)}, nil
	})
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/unspam。
func (a *UnmarkSpam) HTTP(ctx context.Context, in *IDInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
