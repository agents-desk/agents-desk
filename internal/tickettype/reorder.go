package tickettype

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// ReorderParams 是调整工单类型展示顺序的请求。
type ReorderParams struct {
	IDs []string `json:"ids" minItems:"1" doc:"按新顺序排列的工单类型 ID"`
}

// ReorderInput 是调整工单类型顺序的 HTTP 请求。
type ReorderInput struct {
	Body ReorderParams
}

// Reorder 调整当前工作区中工单类型的展示顺序。
type Reorder struct {
	db *bun.DB
}

// NewReorder 返回调整工单类型顺序的动作。
func NewReorder(db *bun.DB) *Reorder { return &Reorder{db: db} }

// Handle 在一个事务中按 p.IDs 的顺序更新 sub 所在工作区中工单类型的 position，并返回排序后的最新列表。
// 出现不存在或属于别的工作区的 ID 时返回 ErrNotFound。
func (a *Reorder) Handle(ctx context.Context, sub identity.Subject, p ReorderParams) ([]TicketTypeDTO, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var rows []TicketType
		if err := tx.NewSelect().Model(&rows).
			Where("tenant_id = ?", sub.Tenant).
			OrderExpr("position, created_at, id").
			For("UPDATE").
			Scan(ctx); err != nil {
			return errors.Wrap(err, "load ticket types for reorder")
		}
		byID := make(map[string]*TicketType, len(rows))
		for i := range rows {
			byID[rows[i].ID] = &rows[i]
		}
		ordered := make([]*TicketType, 0, len(rows))
		seen := make(map[string]struct{}, len(rows))
		for _, id := range p.IDs {
			t, ok := byID[id]
			if !ok {
				return ErrNotFound.New()
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ordered = append(ordered, t)
		}
		for i := range rows {
			if _, ok := seen[rows[i].ID]; !ok {
				ordered = append(ordered, &rows[i])
			}
		}

		changed := false
		for pos, t := range ordered {
			if t.Position == pos {
				continue
			}
			t.Position = pos
			if _, err := tx.NewUpdate().Model(t).Column("position", "updated_at").WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "update ticket type position")
			}
			changed = true
		}
		if !changed {
			return nil
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "ticket_type.reordered", TargetType: "ticket_type", TargetID: sub.Tenant,
			Properties: map[string]any{"ids": p.IDs},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewList(a.db).Handle(ctx, sub, nil)
}

// HTTP 处理 PUT /t/{slug}/api/ticket-types/order。
func (a *Reorder) HTTP(ctx context.Context, in *ReorderInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: items}, nil
}
