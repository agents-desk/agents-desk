package tickettype

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// ListInput 是工单类型列表的查询条件。
type ListInput struct {
	EnabledOnly bool `query:"enabled_only" doc:"为 true 时只返回未停用的工单类型"`
}

// ListOutput 是工单类型列表的响应。
type ListOutput struct {
	Body []TicketTypeDTO
}

// List 列出当前工作区的工单类型，按展示顺序排列。
type List struct {
	db *bun.DB
}

// NewList 返回工单类型列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的工单类型列表；in.EnabledOnly 为 true 时过滤掉已停用的类型。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) ([]TicketTypeDTO, error) {
	var rows []TicketType
	q := a.db.NewSelect().Model(&rows).Where("tenant_id = ?", sub.Tenant).
		OrderExpr("position, created_at, id")
	if in != nil && in.EnabledOnly {
		q = q.Where("disabled_at IS NULL")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list ticket types")
	}
	return toDTOs(ctx, a.db, sub.Tenant, rows)
}

// HTTP 处理 GET /t/{slug}/api/ticket-types。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: items}, nil
}
