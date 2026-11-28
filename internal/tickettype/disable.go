package tickettype

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// Disable 停用当前工作区中的一个工单类型；停用后不能用于新建工单，历史工单保留原类型与字段。
type Disable struct {
	db *bun.DB
}

// NewDisable 返回停用工单类型的动作。
func NewDisable(db *bun.DB) *Disable { return &Disable{db: db} }

// Handle 停用 sub 所在工作区中的工单类型 id；已经停用时幂等返回当前状态。
func (a *Disable) Handle(ctx context.Context, sub identity.Subject, id string) (*TicketTypeDTO, error) {
	var out *TicketType
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := Find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		out = t
		if t.DisabledAt != nil {
			return nil
		}
		at := database.Now()
		t.DisabledAt = &at
		if _, err := tx.NewUpdate().Model(t).Column("disabled_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "disable ticket type")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "ticket_type.disabled", TargetType: "ticket_type", TargetID: id,
			Properties: map[string]any{"name": t.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.db, out)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/ticket-types/{id}/disable。
func (a *Disable) HTTP(ctx context.Context, in *IDInput) (*TicketTypeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &TicketTypeOutput{Body: *dto}, nil
}

// Enable 重新启用当前工作区中已停用的工单类型。
type Enable struct {
	db *bun.DB
}

// NewEnable 返回启用工单类型的动作。
func NewEnable(db *bun.DB) *Enable { return &Enable{db: db} }

// Handle 启用 sub 所在工作区中的工单类型 id；已处于启用状态时幂等返回当前状态。
func (a *Enable) Handle(ctx context.Context, sub identity.Subject, id string) (*TicketTypeDTO, error) {
	var out *TicketType
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := Find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		out = t
		if t.DisabledAt == nil {
			return nil
		}
		t.DisabledAt = nil
		if _, err := tx.NewUpdate().Model(t).Column("disabled_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "enable ticket type")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "ticket_type.enabled", TargetType: "ticket_type", TargetID: id,
			Properties: map[string]any{"name": t.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.db, out)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/ticket-types/{id}/enable。
func (a *Enable) HTTP(ctx context.Context, in *IDInput) (*TicketTypeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &TicketTypeOutput{Body: *dto}, nil
}
