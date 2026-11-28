package agent

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/conversation"
)

// defaultGenerationTTL 是一次生成的默认有效期：超过它仍未结束的生成由定时任务标记为失败，避免一直占着气泡。
const defaultGenerationTTL = 10 * time.Minute

// OpenParams 是创建一次生成的参数。
type OpenParams struct {
	ConversationID string        // 生成所在的会话
	BotID          string        // 产出回复的机器人
	TriggerItemID  string        // 触发这次生成的时间线项
	TriggerSeq     int64         // 触发项的时间线序号
	Mode           Mode          // 生成模式：reply 或 draft，默认 reply
	PromptVersion  string        // 本次使用的提示词版本
	TTL            time.Duration // 有效期；为 0 时用 defaultGenerationTTL
}

// Open 为一条触发项创建一次生成，同一个机器人对同一条触发项幂等返回之前创建的那次生成。
// 会话中若已经存在另一个未结束的生成，返回 ErrAlreadyActive。
type Open struct {
	db *bun.DB
}

// NewOpen 返回创建生成的动作。
func NewOpen(db *bun.DB) *Open { return &Open{db: db} }

// Handle 在 sub 所在的工作区中为 p 创建生成：机器人必须启用，会话必须是服务型或聊天型。
func (a *Open) Handle(ctx context.Context, sub identity.Subject, p OpenParams) (*Generation, error) {
	if sub.Type != identity.System {
		return nil, ErrForbidden.New()
	}
	var bot Bot
	err := a.db.NewSelect().Model(&bot).Where("id = ? AND tenant_id = ? AND enabled = true", p.BotID, sub.Tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBotNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "load bot")
	}
	var kind conversation.Kind
	err = a.db.NewSelect().Table("conversations").Column("kind").
		Where("id = ? AND tenant_id = ?", p.ConversationID, sub.Tenant).Scan(ctx, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, conversation.ErrNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "load conversation for generation")
	}
	if kind != conversation.Service && kind != conversation.Chat {
		return nil, conversation.ErrWrongKind.New()
	}

	// 先查同触发幂等记录：若已存在，直接返回
	var existing Generation
	err = a.db.NewSelect().Model(&existing).
		Where("tenant_id = ? AND conversation_id = ? AND trigger_item_id = ? AND actor_id = ?",
			sub.Tenant, p.ConversationID, p.TriggerItemID, p.BotID).Scan(ctx)
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "load existing generation")
	}

	mode := p.Mode
	if mode == "" {
		mode = ModeReply
	}
	ttl := p.TTL
	if ttl <= 0 {
		ttl = defaultGenerationTTL
	}
	at := now()
	g := &Generation{
		ID:               newID(),
		TenantID:         sub.Tenant,
		ConversationID:   p.ConversationID,
		ActorID:          p.BotID,
		TriggerItemID:    p.TriggerItemID,
		TriggerSeq:       p.TriggerSeq,
		Mode:             mode,
		State:            Pending,
		Parts:            map[string]any{},
		PendingApprovals: []ToolApproval{},
		ToolCalls:        []ToolCallRecord{},
		Citations:        []Citation{},
		PromptVersion:    p.PromptVersion,
		Usage:            map[string]any{},
		ExpiresAt:        at.Add(ttl),
	}
	g.CreatedAt, g.UpdatedAt = at, at
	res, err := a.db.NewInsert().Model(g).On("CONFLICT DO NOTHING").Exec(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "insert generation")
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return g, nil
	}
	// 读回落库的那一条（并发同触发先到，或者已有另一个活跃生成占用单活槽位）
	err = a.db.NewSelect().Model(&existing).
		Where("tenant_id = ? AND conversation_id = ? AND trigger_item_id = ? AND actor_id = ?",
			sub.Tenant, p.ConversationID, p.TriggerItemID, p.BotID).Scan(ctx)
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "load conflicted generation")
	}
	return nil, ErrAlreadyActive.New()
}
