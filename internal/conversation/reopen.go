package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// Reopen 重新打开已关闭或稍后处理的会话。
type Reopen struct {
	db   *bun.DB
	down Downstream
}

// NewReopen 返回重新打开会话的动作；down 是时间线有新内容时通知的下游。
func NewReopen(db *bun.DB, down Downstream) *Reopen { return &Reopen{db: db, down: down} }

// Handle 由 sub 重新打开他所在工作区中的会话 id。已关闭的会话在时间线中追加 conversation.reopened，稍后处理的追加
// conversation.unsnoozed，与对方发来消息时相同；已经打开时什么也不做。
func (a *Reopen) Handle(ctx context.Context, sub identity.Subject, id string) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, reopen(sub))
}

// reopen 返回把会话重新打开的修改，sub 是发起者：稍后处理到期时是系统。
func reopen(sub identity.Subject) changeFunc {
	return func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		if c.Kind != Service {
			return nil, ErrWrongKind.New()
		}
		name := "conversation.reopened"
		switch c.Status {
		case Open:
			return nil, nil
		case Snoozed:
			name = "conversation.unsnoozed"
		}
		from := c.Status
		if from == Closed {
			if err := ensureNoOtherActive(ctx, tx, c); err != nil {
				return nil, err
			}
			at := now()
			c.OpenedAt, c.FirstReplyAt = at, nil
			if err := openRound(ctx, tx, c, at); err != nil {
				return nil, err
			}
		}
		c.Status, c.SnoozedUntil = Open, nil
		return []*Item{event(sub, name, map[string]any{"from": string(from)})}, nil
	}
}

// ensureNoOtherActive 检查对端（联系人同服务域或外部群）下是否已存在其他未关闭（open/snoozed）的服务型会话；
// 存在时返回 ErrActiveConversationExists 并携带当前活跃会话 ID，防止破坏单一活跃会话不变量。
func ensureNoOtherActive(ctx context.Context, tx bun.IDB, c *Conversation) error {
	var activeID string
	var err error
	switch {
	case c.ExternalGroupID != nil && *c.ExternalGroupID != "":
		err = tx.NewSelect().Table("conversations").Column("id").
			Where("tenant_id = ? AND kind = ? AND external_group_id = ? AND id <> ? AND status IN (?)",
				c.TenantID, Service, *c.ExternalGroupID, c.ID, bun.In([]Status{Open, Snoozed})).
			Limit(1).Scan(ctx, &activeID)
	case c.ContactID != nil && *c.ContactID != "":
		err = tx.NewSelect().Table("conversations").Column("id").
			Where("tenant_id = ? AND kind = ? AND audience = ? AND contact_id = ? AND id <> ? AND status IN (?)",
				c.TenantID, Service, c.Audience, *c.ContactID, c.ID, bun.In([]Status{Open, Snoozed})).
			Limit(1).Scan(ctx, &activeID)
	default:
		return nil
	}
	if err == nil && activeID != "" {
		return ErrActiveConversationExists.New(errorx.Public("active_conversation_id", activeID))
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return errors.Wrap(err, "check active conversation before reopen")
	}
	return nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/reopen。
func (a *Reopen) HTTP(ctx context.Context, in *IDInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
