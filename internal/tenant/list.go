package tenant

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/page"
)

// ListInput 是租户列表的查询条件。
type ListInput struct {
	page.OffsetParams
	Name   string `query:"name" maxLength:"60" doc:"名称或 slug 包含的文字"`
	Status Status `query:"status" doc:"只列出这个状态的工作区"`
}

// ListOutput 是一页租户。
type ListOutput struct {
	Body page.Offset[TenantDTO]
}

// List 列出全部租户，供平台管理员使用，按创建时间从新到旧排列。
type List struct {
	db     *bun.DB
	logins *login.Realm // 成员的 login.Realm，读取所有者的邮箱
	addr   Addresses
}

// NewList 返回租户列表的动作。
func NewList(db *bun.DB, logins *login.Realm, addr Addresses) *List {
	return &List{db: db, logins: logins, addr: addr}
}

// HTTP 处理 GET /tenants：按名称或 slug、状态筛选，固定分页。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	var rows []Tenant
	q := a.db.NewSelect().Model(&rows).Relation("Owner").
		OrderExpr("tenant.created_at DESC, tenant.id DESC").
		Limit(in.Limit()).Offset(in.Offset())
	if in.Name != "" {
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("tenant.name ILIKE ?", "%"+in.Name+"%").WhereOr("tenant.slug ILIKE ?", "%"+in.Name+"%")
		})
	}
	switch in.Status {
	case Active:
		q = q.Where("tenant.disabled_at IS NULL")
	case Disabled:
		q = q.Where("tenant.disabled_at IS NOT NULL")
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list tenants")
	}
	items := make([]TenantDTO, 0, len(rows))
	for i := range rows {
		dto, err := toDTO(ctx, a.logins, a.addr, &rows[i])
		if err != nil {
			return nil, err
		}
		items = append(items, dto)
	}
	return &ListOutput{Body: page.NewOffset(items, in.OffsetParams, int64(total))}, nil
}
