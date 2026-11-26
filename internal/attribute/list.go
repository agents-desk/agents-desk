package attribute

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// ListInput 是属性定义列表的查询条件。
type ListInput struct {
	Target       Target `query:"target" doc:"按对象筛选（conversation、contact 或 ticket）；不填返回全部"`
	TicketTypeID string `query:"ticket_type_id" format:"uuid" doc:"按工单类型筛选（配合 target=ticket 使用）"`
}

// ListOutput 是属性定义列表的响应。
type ListOutput struct {
	Body []AttributeDTO
}

// List 列出当前工作区的自定义属性定义，按展示顺序排列。
type List struct {
	db *bun.DB
}

// NewList 返回属性定义列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的属性定义列表；in.Target 或 in.TicketTypeID 非空时按对应维度过滤。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) ([]AttributeDTO, error) {
	var rows []Definition
	q := a.db.NewSelect().Model(&rows).Where("tenant_id = ?", sub.Tenant).
		OrderExpr("target, ticket_type_id NULLS FIRST, position, created_at, id")
	if in != nil && in.Target != "" {
		q = q.Where("target = ?", in.Target)
	}
	if in != nil && in.TicketTypeID != "" {
		q = q.Where("ticket_type_id = ?", in.TicketTypeID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list attribute definitions")
	}
	out := make([]AttributeDTO, len(rows))
	for i := range rows {
		out[i] = rows[i].DTO()
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/attributes。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: items}, nil
}
