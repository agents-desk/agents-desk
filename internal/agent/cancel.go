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

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// Cancel 取消一次进行中的生成：不再产出，也不写时间线。
type Cancel struct {
	db       *bun.DB
	hub      *broadcast.Hub
	onCancel func(ctx context.Context, tenantID, convID string)
}

// NewCancel 返回取消生成的动作；hub 不为 nil 时把取消推送到生成频道。
func NewCancel(db *bun.DB, hub *broadcast.Hub, onCancel ...func(ctx context.Context, tenantID, convID string)) *Cancel {
	var hook func(ctx context.Context, tenantID, convID string)
	if len(onCancel) > 0 {
		hook = onCancel[0]
	}
	return &Cancel{db: db, hub: hub, onCancel: hook}
}

// Handle 由成员 sub 取消工作区中的生成 id，记录取消原因为 manual。
func (a *Cancel) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return a.HandleWithReason(ctx, sub, id, "manual")
}

// HandleWithReason 由成员或系统主体取消生成并记录 cancel_reason。
func (a *Cancel) HandleWithReason(ctx context.Context, sub identity.Subject, id, reason string) error {
	switch sub.Type {
	case identity.System:
	case member.Type:
		if !sub.Can(string(auth.ConversationsRead)) {
			return ErrForbidden.New()
		}
	default:
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
	if sub.Type == member.Type {
		visible, err := conversation.CanView(ctx, a.db, sub, g.ConversationID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrNotFound.New()
		}
	}
	if strings.TrimSpace(reason) == "" {
		reason = "manual"
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
		g.CancelReason = reason
		g.UpdatedAt = now()
		if _, err := tx.NewUpdate().Model(&g).Column("state", "cancel_reason", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "cancel generation")
		}
		if a.onCancel != nil {
			cb, tid, cid := a.onCancel, g.TenantID, g.ConversationID
			tx.AfterCommit(func(ctx context.Context) error {
				cb(ctx, tid, cid)
				return nil
			})
		}
		if a.hub != nil {
			hub, frame := a.hub, Frame{
				Type: "canceled", GenerationID: g.ID, ConversationID: g.ConversationID, Mode: g.Mode,
				Checkpoint: g.Checkpoint, State: g.State, CancelReason: g.CancelReason,
			}
			tx.AfterCommit(func(ctx context.Context) error {
				return errors.Wrap(hub.PublishJSON(ctx, Channel(g.TenantID, g.ID), frame), "publish generation canceled")
			})
		}
		return nil
	})
}
