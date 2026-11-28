package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
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

// Handle 由 sub 关闭他所在工作区中的会话 id：把当前进行中的服务轮次标记为已关闭并写入归属与解决口径快照，
// 记下会话的关闭时间，清空稍后处理的时间，取消尚未定稿的生成，时间线中追加 conversation.closed。
// 已经关闭时什么也不做。团队收件箱和负责人保留，报表据此统计是谁处理的。
func (a *Close) Handle(ctx context.Context, sub identity.Subject, id string) (*ConversationDTO, error) {
	return changeTx(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx database.Tx, c *Conversation) ([]*Item, error) {
		return applyClose(ctx, tx, a.down, sub, c)
	})
}

// applyClose 在事务 tx 中关闭会话 c、计算本轮解决归因与 AI 生成次数并更新服务轮次快照，然后取消活跃生成并返回系统事件；已关闭时返回 nil。
func applyClose(ctx context.Context, tx database.Tx, down Downstream, sub identity.Subject, c *Conversation) ([]*Item, error) {
	if c.Kind != Service {
		return nil, ErrWrongKind.New()
	}
	if c.Status == Closed {
		return nil, nil
	}
	if _, err := CancelActiveGenerationsInTx(ctx, tx, down, c.TenantID, c.ID, "conversation_closed"); err != nil {
		return nil, err
	}
	at := database.Now()
	resType, genCount, err := computeRoundResolution(ctx, tx, c)
	if err != nil {
		return nil, err
	}
	var current Round
	err = tx.NewSelect().Model(&current).
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
		current.ResolutionType = resType
		current.AIGenerationCount = genCount
		current.UpdatedAt = at
		if _, err := tx.NewUpdate().Model(&current).
			Column("team_id", "assignee_type", "assignee_id", "closed_by_type", "closed_by_id", "first_reply_at", "closed_at", "resolution_type", "ai_generation_count", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "close conversation round")
		}
	case errors.Is(err, sql.ErrNoRows):
		var round int
		if err := tx.NewRaw("SELECT coalesce(max(round), 0) + 1 FROM conversation_rounds WHERE conversation_id = ?", c.ID).Scan(ctx, &round); err != nil {
			return nil, errors.Wrap(err, "next conversation round")
		}
		r := &Round{
			ID:                database.NewID(),
			TenantID:          c.TenantID,
			ConversationID:    c.ID,
			Round:             round,
			TeamID:            c.TeamID,
			AssigneeType:      c.AssigneeType,
			AssigneeID:        c.AssigneeID,
			ClosedByType:      actorOf(sub),
			ClosedByID:        sub.ID,
			OpenedAt:          c.OpenedAt,
			FirstReplyAt:      c.FirstReplyAt,
			ClosedAt:          &at,
			ResolutionType:    resType,
			AIGenerationCount: genCount,
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

// computeRoundResolution 统计本轮（自 c.OpenedAt 起）AI 生成数量并按真实对外回复发起人计算解决归因。
func computeRoundResolution(ctx context.Context, tx bun.IDB, c *Conversation) (string, int, error) {
	var stats struct {
		GenCount  int  `bun:"gen_count"`
		HasBot    bool `bun:"has_bot"`
		HasMember bool `bun:"has_member"`
	}
	err := tx.NewRaw(`SELECT
		(SELECT count(*) FROM generations g WHERE g.tenant_id = ? AND g.conversation_id = ? AND g.created_at >= ?) AS gen_count,
		EXISTS (
			SELECT 1 FROM timeline_items ti
			WHERE ti.tenant_id = ? AND ti.conversation_id = ? AND ti.created_at >= ?
				AND ti.kind = ? AND ti.visibility = ? AND ti.actor_type = ?
				AND NOT EXISTS (SELECT 1 FROM message_recalls mr WHERE mr.item_id = ti.id)
		) AS has_bot,
		EXISTS (
			SELECT 1 FROM timeline_items ti
			WHERE ti.tenant_id = ? AND ti.conversation_id = ? AND ti.created_at >= ?
				AND ti.kind = ? AND ti.visibility = ? AND ti.actor_type = ?
				AND NOT EXISTS (SELECT 1 FROM message_recalls mr WHERE mr.item_id = ti.id)
		) AS has_member`,
		c.TenantID, c.ID, c.OpenedAt,
		c.TenantID, c.ID, c.OpenedAt, Message, Everyone, BotActor,
		c.TenantID, c.ID, c.OpenedAt, Message, Everyone, MemberActor,
	).Scan(ctx, &stats)
	if err != nil {
		return "", 0, errors.Wrap(err, "compute round resolution")
	}
	switch {
	case stats.HasBot && !stats.HasMember:
		return ResolutionAIResolved, stats.GenCount, nil
	case (stats.HasBot || stats.GenCount > 0) && stats.HasMember:
		return ResolutionAIAssisted, stats.GenCount, nil
	default:
		return ResolutionHumanResolved, stats.GenCount, nil
	}
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
