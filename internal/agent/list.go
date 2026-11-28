package agent

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// List 读取一个会话中还在进行的生成（含待审批），以及最新一条尚未采纳的副驾驶草稿。
type List struct {
	db *bun.DB
}

// NewList 返回读取生成的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回成员 sub 能看到会话 conversationID 中进行中的生成及最新未采纳草稿，按创建时间排序。
func (a *List) Handle(ctx context.Context, sub identity.Subject, conversationID string) ([]GenerationDTO, error) {
	if sub.Type != member.Type || !sub.Can(string(auth.ConversationsRead)) {
		return nil, ErrForbidden.New()
	}
	visible, err := conversation.CanView(ctx, a.db, sub, conversationID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, conversation.ErrNotFound.New()
	}

	var gs []Generation
	err = a.db.NewSelect().Model(&gs).
		Where("tenant_id = ? AND conversation_id = ? AND state IN (?)", sub.Tenant, conversationID,
			bun.In([]State{Pending, Streaming, WaitingApproval})).
		OrderExpr("created_at, id").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list active generations")
	}

	// 同时读取最新一条已完成的 Copilot 草稿（未在 timeline_items 中被成员回复采纳时返回供前端展示与采纳）
	var draft Generation
	err = a.db.NewSelect().Model(&draft).
		Where("tenant_id = ? AND conversation_id = ? AND mode = ? AND state = ?", sub.Tenant, conversationID, ModeDraft, Completed).
		Where("NOT EXISTS (SELECT 1 FROM timeline_items ti WHERE ti.generation_id = generation.id)").
		OrderExpr("created_at DESC, id DESC").Limit(1).Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "load latest copilot draft")
	}
	if err == nil && draft.ID != "" {
		gs = append(gs, draft)
	}

	actors, err := a.actors(ctx, sub.Tenant, gs)
	if err != nil {
		return nil, err
	}
	out := make([]GenerationDTO, len(gs))
	for i := range gs {
		out[i] = gs[i].dto(actors[gs[i].ActorID])
	}
	return out, nil
}

// actors 返回这些生成对应的机器人资料，键是机器人 ID；机器人不存在或已删除时给出空资料。
func (a *List) actors(ctx context.Context, tenantID string, gs []Generation) (map[string]conversation.Actor, error) {
	seen := map[string]bool{}
	var ids []string
	for _, g := range gs {
		if !seen[g.ActorID] {
			seen[g.ActorID] = true
			ids = append(ids, g.ActorID)
		}
	}
	out := map[string]conversation.Actor{}
	if len(ids) == 0 {
		return out, nil
	}
	var bots []Bot
	if err := a.db.NewSelect().Model(&bots).
		Where("tenant_id = ? AND id IN (?)", tenantID, bun.In(ids)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load generation bots")
	}
	for _, b := range bots {
		out[b.ID] = conversation.Actor{Type: conversation.BotActor, ID: b.ID, Name: b.Name}
	}
	return out, nil
}
