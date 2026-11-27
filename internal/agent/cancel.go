package agent

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// Cancel 取消一次进行中的生成：不再产出，也不写时间线。
type Cancel struct {
	db  *bun.DB
	hub *broadcast.Hub
}

// NewCancel 返回取消生成的动作；hub 不为 nil 时把取消推送到生成频道。
func NewCancel(db *bun.DB, hub *broadcast.Hub) *Cancel { return &Cancel{db: db, hub: hub} }

// Handle 由成员 sub 取消工作区中的生成 id：只有能看到它所在会话的成员可以取消。
// 已经取消时幂等返回；已经定稿或失败时返回 ErrFinished。
func (a *Cancel) Handle(ctx context.Context, sub identity.Subject, id string) error {
	if sub.Type != member.Type || !sub.Can(string(auth.ConversationsRead)) {
		return ErrForbidden.New()
	}
	var g Generation
	err := a.db.NewSelect().Model(&g).Where("id = ? AND tenant_id = ?", id, sub.Tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound.New()
	}
	if err != nil {
		return errors.Wrap(err, "load generation")
	}
	visible, err := conversation.CanView(ctx, a.db, sub, g.ConversationID)
	if err != nil {
		return err
	}
	if !visible {
		return ErrNotFound.New()
	}
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := tx.NewSelect().Model(&g).
			Where("id = ? AND tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return errors.Wrap(err, "lock generation")
		}
		if g.State == Canceled {
			return nil
		}
		if !g.State.active() {
			return ErrFinished.New()
		}
		g.State = Canceled
		g.UpdatedAt = now()
		if _, err := tx.NewUpdate().Model(&g).Column("state", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "cancel generation")
		}
		if a.hub != nil {
			hub, frame := a.hub, Frame{
				Type: "canceled", GenerationID: g.ID, ConversationID: g.ConversationID,
				Checkpoint: g.Checkpoint, State: g.State,
			}
			tx.AfterCommit(func(ctx context.Context) error {
				return errors.Wrap(hub.PublishJSON(ctx, Channel(g.TenantID, g.ID), frame), "publish generation canceled")
			})
		}
		return nil
	})
}
