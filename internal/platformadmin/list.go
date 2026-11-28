package platformadmin

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/page"
)

// ListInput 是平台管理员列表的查询条件。
type ListInput struct {
	page.OffsetParams
	Name   string `query:"name" maxLength:"60" doc:"名字包含的文字"`
	Status Status `query:"status" doc:"只列出这个状态的平台管理员"`
}

// ListOutput 是一页平台管理员。
type ListOutput struct {
	Body page.Offset[AdminDTO]
}

// List 列出全部平台管理员，按创建时间从新到旧排列。
type List struct {
	db     *bun.DB
	logins *login.Realm
}

// NewList 返回平台管理员列表的动作。
func NewList(db *bun.DB, logins *login.Realm) *List {
	return &List{db: db, logins: logins}
}

// HTTP 处理 GET /admins：按名字和状态筛选，固定分页。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	var rows []Admin
	q := a.db.NewSelect().Model(&rows).
		OrderExpr("created_at DESC, id DESC").
		Limit(in.Limit()).Offset(in.Offset())
	if in.Name != "" {
		q = q.Where(`name ILIKE ? ESCAPE '\'`, "%"+database.EscapeLike(in.Name)+"%")
	}
	switch in.Status {
	case Active:
		q = q.Where("disabled_at IS NULL")
	case Disabled:
		q = q.Where("disabled_at IS NOT NULL")
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list platform admins")
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	emails, err := a.logins.IdentifiersMany(ctx, ids, login.KindEmail)
	if err != nil {
		return nil, err
	}
	items := make([]AdminDTO, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		items = append(items, AdminDTO{
			ID: r.ID, Name: r.Name, Email: emails[r.ID], Status: r.Status(),
			Self: r.ID == sub.ID, CreatedAt: r.CreatedAt,
		})
	}
	return &ListOutput{Body: page.NewOffset(items, in.OffsetParams, int64(total))}, nil
}
