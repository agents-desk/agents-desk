package agent

import (
	"context"
	"database/sql"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/conversation"
)

// FinalizeParams 是定稿一次生成的参数。
type FinalizeParams struct {
	GenerationID   string
	Text           string           // 完整的回复文字
	Usage          map[string]any   // 可选的模型用量
	ToolCalls      []ToolCallRecord // 审计工具调用列表
	Citations      []Citation       // 知识库检索引用
	HandoverReason string           // 非空时定稿后自动暂停 AI 并转入人工团队收件箱
}

// Finalize 把生成定稿为时间线消息（reply）或副驾驶草稿（draft）：状态与消息在同一事务内提交。
type Finalize struct {
	db   *bun.DB
	down conversation.Downstream
	hub  *broadcast.Hub
}

// NewFinalize 返回定稿生成的动作；down 决定定稿后通知哪些下游，hub 用于把定稿结果推送到生成频道。
func NewFinalize(db *bun.DB, down conversation.Downstream, hub *broadcast.Hub) *Finalize {
	return &Finalize{db: db, down: down, hub: hub}
}

// Handle 在 sub 所在的工作区中把生成 p.GenerationID 定稿。
// reply 模式写入一条带 generation_id 和 after_seq 锚点的机器人消息；draft 模式仅保留已完成草稿不写入时间线。
// 若模型请求转人工，同事务把会话切为 ai_paused 并释放 Bot 指派。
func (a *Finalize) Handle(ctx context.Context, sub identity.Subject, p FinalizeParams) (*conversation.Item, error) {
	if sub.Type != identity.System {
		return nil, ErrForbidden.New()
	}
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return nil, ErrEmptyReply.New()
	}
	var (
		g         Generation
		item      conversation.Item
		hasItem   bool
		finalized bool
	)
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := tx.NewSelect().Model(&g).
			Where("id = ? AND tenant_id = ?", p.GenerationID, sub.Tenant).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return errors.Wrap(err, "load generation")
		}
		if g.State == Completed {
			if g.ItemID != nil {
				hasItem = true
				return errors.Wrap(tx.NewSelect().Model(&item).
					Where("id = ? AND tenant_id = ?", *g.ItemID, g.TenantID).Scan(ctx), "load finalized item")
			}
			return nil
		}
		if !g.State.active() {
			return ErrFinished.New()
		}
		at := now()
		mode := g.Mode
		if mode == "" {
			mode = ModeReply
		}
		if mode == ModeReply {
			triggerSeq := g.TriggerSeq
			item = conversation.Item{
				Kind:         conversation.Message,
				ActorType:    conversation.BotActor,
				ActorID:      g.ActorID,
				Visibility:   conversation.Everyone,
				AfterSeq:     &triggerSeq,
				GenerationID: &g.ID,
				Content:      conversation.Content{Text: text},
			}
			if err := conversation.AppendAgentMessage(ctx, tx, a.down, g.TenantID, g.ConversationID, &item, at); err != nil {
				return err
			}
			g.ItemID = &item.ID
			hasItem = true
		}

		if p.HandoverReason != "" {
			if err := conversation.SetHandlingModeInTx(ctx, tx, a.down, g.TenantID, g.ConversationID, conversation.HandlingAIPaused, p.HandoverReason, true); err != nil {
				return err
			}
		} else if g.State == WaitingApproval {
			// 工具审批完成后恢复接待模式（ai_auto 下切回 ai_auto，否则按当前状态）
			var currentMode conversation.HandlingMode
			if err := tx.NewSelect().Table("conversations").Column("handling_mode").
				Where("id = ? AND tenant_id = ?", g.ConversationID, g.TenantID).Scan(ctx, &currentMode); err != nil {
				return errors.Wrap(err, "load conversation handling mode")
			}
			if currentMode == conversation.HandlingWaitingApproval {
				restore := conversation.HandlingAIAuto
				if mode == ModeDraft {
					restore = conversation.HandlingAICopilot
				}
				if err := conversation.SetHandlingModeInTx(ctx, tx, a.down, g.TenantID, g.ConversationID, restore, "", false); err != nil {
					return err
				}
			}
		}

		// 在开放的服务轮次中累加 AI 生成次数，供关闭归因统计使用
		if _, err := tx.NewRaw(`UPDATE conversation_rounds
			SET ai_generation_count = ai_generation_count + 1
			WHERE conversation_id = ? AND closed_at IS NULL`, g.ConversationID).Exec(ctx); err != nil {
			return errors.Wrap(err, "increment round ai_generation_count")
		}

		g.State = Completed
		g.Parts = map[string]any{"text": text}
		g.Checkpoint++
		g.UpdatedAt = at
		if p.Usage != nil {
			g.Usage = p.Usage
		}
		if p.ToolCalls != nil {
			g.ToolCalls = p.ToolCalls
		}
		if p.Citations != nil {
			g.Citations = p.Citations
		}
		if _, err := tx.NewUpdate().Model(&g).
			Column("state", "item_id", "parts", "checkpoint_seq", "usage", "tool_calls", "citations", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save finalized generation")
		}
		finalized = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if finalized && a.hub != nil {
		frame := Frame{
			Type: "done", GenerationID: g.ID, ConversationID: g.ConversationID, Mode: g.Mode,
			ItemID: item.ID, Seq: item.Seq, Checkpoint: g.Checkpoint, State: g.State, Citations: g.Citations,
		}
		if err := a.hub.PublishJSON(ctx, Channel(g.TenantID, g.ID), frame); err != nil {
			return nil, errors.Wrap(err, "publish generation done")
		}
	}
	if !hasItem {
		return nil, nil
	}
	return &item, nil
}
