package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// Close 关闭会话：处理完毕。对方再回复时按渠道规则重新打开或新建会话。
type Close struct {
	db   *bun.DB
	down Downstream
}

// NewClose 返回关闭会话的动作；down 是时间线有新内容时通知的下游。
func NewClose(db *bun.DB, down Downstream) *Close { return &Close{db: db, down: down} }

// Handle 由 sub 关闭他所在工作区中的会话 id：把当前进行中的服务轮次标记为已关闭并写入归属快照，
// 记下会话的关闭时间，清空稍后处理的时间，时间线中追加 conversation.closed。
// 已经关闭时什么也不做。团队收件箱和负责人保留，报表据此统计是谁处理的。
func (a *Close) Handle(ctx context.Context, sub identity.Subject, id string) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		return applyClose(ctx, tx, sub, c)
	})
}

// applyClose 在事务 tx 中关闭会话 c、更新当前服务轮次的关闭时间与归属快照并返回系统事件；已关闭时返回 nil。
func applyClose(ctx context.Context, tx bun.IDB, sub identity.Subject, c *Conversation) ([]*Item, error) {
	if c.Kind != Service {
		return nil, ErrWrongKind.New()
	}
	if c.Status == Closed {
		return nil, nil
	}
	at := now()
	var current Round
	err := tx.NewSelect().Model(&current).
		Where("tenant_id = ? AND conversation_id = ? AND closed_at IS NULL", c.TenantID, c.ID).
		OrderExpr("round DESC").Limit(1).For("UPDATE").Scan(ctx)
	switch {
	case err == nil:
		current.TeamID = c.TeamID
		current.AssigneeType = c.AssigneeType
		current.AssigneeID = c.AssigneeID
		current.ClosedByType = actorOf(sub)
		current.ClosedByID = sub.ID
		if current.FirstReplyAt == nil && c.FirstReplyAt != nil {
			current.FirstReplyAt = c.FirstReplyAt
		}
		current.ClosedAt = &at
		current.UpdatedAt = at
		if _, err := tx.NewUpdate().Model(&current).
			Column("team_id", "assignee_type", "assignee_id", "closed_by_type", "closed_by_id", "first_reply_at", "closed_at", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "close conversation round")
		}
	case errors.Is(err, sql.ErrNoRows):
		var round int
		if err := tx.NewRaw("SELECT coalesce(max(round), 0) + 1 FROM conversation_rounds WHERE conversation_id = ?", c.ID).Scan(ctx, &round); err != nil {
			return nil, errors.Wrap(err, "next conversation round")
		}
		r := &Round{
			ID:             newID(),
			TenantID:       c.TenantID,
			ConversationID: c.ID,
			Round:          round,
			TeamID:         c.TeamID,
			AssigneeType:   c.AssigneeType,
			AssigneeID:     c.AssigneeID,
			ClosedByType:   actorOf(sub),
			ClosedByID:     sub.ID,
			OpenedAt:       c.OpenedAt,
			FirstReplyAt:   c.FirstReplyAt,
			ClosedAt:       &at,
		}
		if _, err := tx.NewInsert().Model(r).Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "insert conversation round")
		}
	default:
		return nil, errors.Wrap(err, "load open conversation round")
	}
	from := c.Status
	c.Status, c.ClosedAt, c.SnoozedUntil = Closed, &at, nil
	return []*Item{event(sub, "conversation.closed", map[string]any{"from": string(from)})}, nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/close。
func (a *Close) HTTP(ctx context.Context, in *IDInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
