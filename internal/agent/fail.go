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

// FailParams 是标记一次生成失败的参数。
type FailParams struct {
	GenerationID string
	Reason       string // 失败原因，写入 generations.error，便于排障
}

// Fail 把进行中的生成标记为失败：不产生消息，已产出的内容留在 generations 里供排查。
type Fail struct {
	db  *bun.DB
	hub *broadcast.Hub
}

// NewFail 返回标记生成失败的动作；hub 不为 nil 时把失败推送到生成频道。
func NewFail(db *bun.DB, hub *broadcast.Hub) *Fail { return &Fail{db: db, hub: hub} }

// Handle 由系统主体把生成 p.GenerationID 标记为失败；已经失败时幂等返回，已经定稿或取消时返回 ErrFinished。
func (a *Fail) Handle(ctx context.Context, sub identity.Subject, p FailParams) error {
	if sub.Type != identity.System {
		return ErrForbidden.New()
	}
	var g Generation
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := tx.NewSelect().Model(&g).
			Where("id = ? AND tenant_id = ?", p.GenerationID, sub.Tenant).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return errors.Wrap(err, "load generation")
		}
		if g.State == Failed {
			return nil
		}
		if !g.State.active() {
			return ErrFinished.New()
		}
		g.State = Failed
		g.Error = p.Reason
		g.UpdatedAt = now()
		if _, err := tx.NewUpdate().Model(&g).Column("state", "error", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "fail generation")
		}
		if a.hub != nil {
			hub, frame := a.hub, Frame{
				Type: "failed", GenerationID: g.ID, ConversationID: g.ConversationID,
				Checkpoint: g.Checkpoint, State: g.State,
			}
			tx.AfterCommit(func(ctx context.Context) error {
				return errors.Wrap(hub.PublishJSON(ctx, Channel(g.TenantID, g.ID), frame), "publish generation failed")
			})
		}
		return nil
	})
}
