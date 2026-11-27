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
	GenerationID string
	Text         string         // 完整的回复文字
	Usage        map[string]any // 可选的模型用量
}

// Finalize 把生成定稿为时间线中的一条消息：消息与“已完成”的状态在同一个事务里提交。
type Finalize struct {
	db   *bun.DB
	down conversation.Downstream
	hub  *broadcast.Hub
}

// NewFinalize 返回定稿生成的动作；down 决定定稿后通知哪些下游，hub 用于把定稿结果推送到生成频道。
func NewFinalize(db *bun.DB, down conversation.Downstream, hub *broadcast.Hub) *Finalize {
	return &Finalize{db: db, down: down, hub: hub}
}

// Handle 在 sub 所在的工作区中把生成 p.GenerationID 定稿为一条机器人消息。
// 已经定稿时幂等返回之前那条消息；已取消或失败时返回 ErrFinished；空回复返回 ErrEmptyReply。
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
		// 重复定稿（任务重试）返回已经写入的那条消息
		if g.State == Completed && g.ItemID != nil {
			return errors.Wrap(tx.NewSelect().Model(&item).
				Where("id = ? AND tenant_id = ?", *g.ItemID, g.TenantID).Scan(ctx), "load finalized item")
		}
		if !g.State.active() {
			return ErrFinished.New()
		}
		item = conversation.Item{
			Kind:       conversation.Message,
			ActorType:  conversation.BotActor,
			ActorID:    g.ActorID,
			Visibility: conversation.Everyone,
			Content:    conversation.Content{Text: text},
		}
		if err := conversation.AppendAgentMessage(ctx, tx, a.down, g.TenantID, g.ConversationID, &item, now()); err != nil {
			return err
		}
		g.State = Completed
		g.ItemID = &item.ID
		g.Parts = map[string]any{"text": text}
		g.Checkpoint++
		g.UpdatedAt = now()
		if p.Usage != nil {
			g.Usage = p.Usage
		}
		if _, err := tx.NewUpdate().Model(&g).
			Column("state", "item_id", "parts", "checkpoint_seq", "usage", "updated_at").WherePK().Exec(ctx); err != nil {
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
			Type: "done", GenerationID: g.ID, ConversationID: g.ConversationID,
			ItemID: item.ID, Seq: item.Seq, Checkpoint: g.Checkpoint, State: g.State,
		}
		if err := a.hub.PublishJSON(ctx, Channel(g.TenantID, g.ID), frame); err != nil {
			return nil, errors.Wrap(err, "publish generation done")
		}
	}
	return &item, nil
}
