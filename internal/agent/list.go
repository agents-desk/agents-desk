package agent

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// List 读取一个会话中还在进行的生成：页面打开或断线重连时用它恢复气泡。
type List struct {
	db *bun.DB
}

// NewList 返回读取进行中生成的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回成员 sub 能看到会话 conversationID 中全部进行中的生成，按创建时间排序。
// 会话不存在或看不到时返回 conversation.ErrNotFound，不暴露它是否存在。
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
