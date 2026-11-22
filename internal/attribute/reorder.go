package attribute

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// ReorderParams 是调整属性展示顺序的请求。
type ReorderParams struct {
	Target Target   `json:"target" doc:"属性所属对象（conversation 或 contact）"`
	IDs    []string `json:"ids" minItems:"1" doc:"按新顺序排列的属性定义 ID"`
}

// ReorderInput 是调整属性展示顺序的 HTTP 请求。
type ReorderInput struct {
	Body ReorderParams
}

// Reorder 调整当前工作区中指定对象下自定义属性的展示顺序。
type Reorder struct {
	db *bun.DB
}

// NewReorder 返回调整属性顺序的动作。
func NewReorder(db *bun.DB) *Reorder { return &Reorder{db: db} }

// Handle 在一个事务中按 p.IDs 的顺序更新 sub 所在工作区中对象 p.Target 的属性 position，
// 并返回排序后的最新列表。出现不存在或属于别的工作区/对象的 ID 时返回 ErrNotFound。
func (a *Reorder) Handle(ctx context.Context, sub identity.Subject, p ReorderParams) ([]AttributeDTO, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var rows []Definition
		if err := tx.NewSelect().Model(&rows).
			Where("tenant_id = ? AND target = ?", sub.Tenant, p.Target).
			OrderExpr("position, created_at, id").
			For("UPDATE").
			Scan(ctx); err != nil {
			return errors.Wrap(err, "load attribute definitions for reorder")
		}
		byID := make(map[string]*Definition, len(rows))
		for i := range rows {
			byID[rows[i].ID] = &rows[i]
		}

		// 先按传入的 IDs 排列，去重并校验归属；如果有并发新建的未传入项，顺延排在后面
		ordered := make([]*Definition, 0, len(rows))
		seen := make(map[string]struct{}, len(rows))
		for _, id := range p.IDs {
			d, ok := byID[id]
			if !ok {
				return ErrNotFound.New()
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ordered = append(ordered, d)
		}
		for i := range rows {
			if _, ok := seen[rows[i].ID]; !ok {
				ordered = append(ordered, &rows[i])
			}
		}

		changed := false
		for pos, d := range ordered {
			if d.Position == pos {
				continue
			}
			d.Position = pos
			if _, err := tx.NewUpdate().Model(d).Column("position", "updated_at").WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "update attribute position")
			}
			changed = true
		}
		if !changed {
			return nil
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "attribute.reordered", TargetType: "attribute", TargetID: string(p.Target),
			Properties: map[string]any{"target": string(p.Target), "ids": p.IDs},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewList(a.db).Handle(ctx, sub, &ListInput{Target: p.Target})
}

// HTTP 处理 PUT /t/{slug}/api/attributes/order。
func (a *Reorder) HTTP(ctx context.Context, in *ReorderInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: items}, nil
}
