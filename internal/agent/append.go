package agent

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// AppendParams 是写入一段已产出内容的参数。
type AppendParams struct {
	GenerationID string
	Delta        string // 新产出的一段文字
}

// Append 把模型新产出的一段文字写进生成，并推送到生成频道。
type Append struct {
	db  *bun.DB
	hub *broadcast.Hub
}

// NewAppend 返回写入生成内容的动作；hub 不为 nil 时把片段推送到生成频道。
func NewAppend(db *bun.DB, hub *broadcast.Hub) *Append { return &Append{db: db, hub: hub} }

// Handle 在 sub 所在的工作区中把 p.Delta 追加到生成 p.GenerationID 已产出的文字后面。
// 生成已经结束时返回 ErrFinished，由调用方停下；片段只送给订阅生成频道的人，不写时间线。
func (a *Append) Handle(ctx context.Context, sub identity.Subject, p AppendParams) (*Generation, error) {
	if sub.Type != identity.System {
		return nil, ErrForbidden.New()
	}
	var g Generation
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := tx.NewSelect().Model(&g).
			Where("id = ? AND tenant_id = ?", p.GenerationID, sub.Tenant).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return errors.Wrap(err, "load generation")
		}
		if !g.State.active() {
			return ErrFinished.New()
		}
		if g.Parts == nil {
			g.Parts = map[string]any{}
		}
		g.Parts["text"] = g.text() + p.Delta
		g.State = Streaming
		g.Checkpoint++
		g.UpdatedAt = now()
		_, err := tx.NewUpdate().Model(&g).
			Column("state", "parts", "checkpoint_seq", "updated_at").WherePK().Exec(ctx)
		return errors.Wrap(err, "save generation chunk")
	})
	if err != nil {
		return nil, err
	}
	if a.hub != nil {
		frame := Frame{
			Type: "delta", GenerationID: g.ID, ConversationID: g.ConversationID,
			Delta: p.Delta, Checkpoint: g.Checkpoint, State: g.State,
		}
		if err := a.hub.PublishJSON(ctx, Channel(g.TenantID, g.ID), frame); err != nil {
			return nil, errors.Wrap(err, "publish generation delta")
		}
	}
	return &g, nil
}
