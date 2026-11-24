package team

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
)

// ListInput 是团队收件箱列表的查询条件。
type ListInput struct {
	page.OffsetParams
	Name   string `query:"name" maxLength:"60" doc:"名称包含的文字"`
	Status Status `query:"status" doc:"按状态筛选：正常或已停用"`
	Mine   bool   `query:"mine" doc:"只列出我所在的团队收件箱"`
}

// ListOutput 是一页团队收件箱。
type ListOutput struct {
	Body page.Offset[TeamDTO]
}

// List 列出当前工作区的团队收件箱，按名称排列。
type List struct {
	db *bun.DB
}

// NewList 返回团队收件箱列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的团队收件箱，可以按名称筛选，in.Mine 时只列出 sub 所在的团队收件箱。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[TeamDTO], error) {
	var rows []Team
	q := a.db.NewSelect().Model(&rows).Where("tenant_id = ?", sub.Tenant).
		OrderExpr("name, id").Limit(in.Limit()).Offset(in.Offset())
	if in.Name != "" {
		q = q.Where(`name ILIKE ? ESCAPE '\'`, "%"+database.EscapeLike(in.Name)+"%")
	}
	switch in.Status {
	case Active:
		q = q.Where("disabled_at IS NULL")
	case Disabled:
		q = q.Where("disabled_at IS NOT NULL")
	}
	if in.Mine {
		q = q.Where("id IN (SELECT team_id FROM team_members WHERE member_id = ?)", sub.ID)
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list teams")
	}
	items, err := toDTOs(ctx, a.db, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/teams。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
