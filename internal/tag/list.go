package tag

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
)

// ListInput 是标签列表的查询条件。
type ListInput struct {
	page.OffsetParams
	Name string `query:"name" maxLength:"60" doc:"名称包含的文字"`
}

// ListOutput 是一页标签。
type ListOutput struct {
	Body page.Offset[TagDTO]
}

// List 列出当前工作区的标签，按名称排列。
type List struct {
	db *bun.DB
}

// NewList 返回标签列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的标签，可以按名称筛选。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[TagDTO], error) {
	var rows []Tag
	q := a.db.NewSelect().Model(&rows).Where("tenant_id = ?", sub.Tenant).
		OrderExpr("lower(name), id").Limit(in.Limit()).Offset(in.Offset())
	if in.Name != "" {
		q = q.Where(`name ILIKE ? ESCAPE '\'`, "%"+database.EscapeLike(in.Name)+"%")
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list tags")
	}
	items, err := toDTOs(ctx, a.db, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/tags。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
