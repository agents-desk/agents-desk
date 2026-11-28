package organization

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
)

// ListInput 是客户公司/组织列表的查询参数。
type ListInput struct {
	page.OffsetParams
	Q string `query:"q" maxLength:"100" doc:"按组织名称、域名或外部客户编号模糊搜索"`
}

// ListOutput 是一页客户公司/组织列表的 HTTP 响应。
type ListOutput struct {
	Body page.Offset[DTO]
}

// List 分页列出工作区中的客户公司/组织。
type List struct {
	db *bun.DB
}

// NewList 返回组织列表动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 按关键词与分页参数返回 sub 所在工作区的组织列表及关联计数。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[DTO], error) {
	if in == nil {
		in = &ListInput{}
	}
	var rows []Organization
	q := a.db.NewSelect().Model(&rows).
		Where("org.tenant_id = ?", sub.Tenant).
		OrderExpr("org.created_at DESC, org.id DESC").
		Limit(in.Limit()).Offset(in.Offset())
	if kw := strings.TrimSpace(in.Q); kw != "" {
		like := "%" + database.EscapeLike(kw) + "%"
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where(`org.name ILIKE ? ESCAPE '\'`, like).
				WhereOr(`org.domain ILIKE ? ESCAPE '\'`, like).
				WhereOr(`org.external_id ILIKE ? ESCAPE '\'`, like)
		})
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list organizations")
	}
	items, err := buildDTOs(ctx, a.db, sub.Tenant, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/organizations。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
