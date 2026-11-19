package tenant

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/identity"
)

// MineItem 是成员所属的一个工作区。已停用的也列出，界面中显示为已停用，不能进入。
type MineItem struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	URL    string `json:"url" doc:"成员进入工作区的地址"`
	Status Status `json:"status"`
}

// MineOutput 是成员所属的全部工作区。
type MineOutput struct {
	Body []MineItem
}

// Mine 列出成员所属的工作区：登录后的去处和切换工作区的菜单用它。
type Mine struct {
	db    *bun.DB
	roles *authz.Store
	addr  Addresses
}

// NewMine 返回列出成员所属工作区的动作。
func NewMine(db *bun.DB, roles *authz.Store, addr Addresses) *Mine {
	return &Mine{db: db, roles: roles, addr: addr}
}

// Handle 返回 sub 有角色的全部工作区，按名称排序。
func (a *Mine) Handle(ctx context.Context, sub identity.Subject) ([]MineItem, error) {
	ids, err := a.roles.Tenants(ctx, sub.Ref())
	if err != nil {
		return nil, err
	}
	items := []MineItem{}
	if len(ids) == 0 {
		return items, nil
	}
	var tenants []Tenant
	if err := a.db.NewSelect().Model(&tenants).Where("id IN (?)", bun.In(ids)).Order("name", "slug").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list member tenants")
	}
	for _, t := range tenants {
		items = append(items, MineItem{Slug: t.Slug, Name: t.Name, URL: a.addr.URL(&t), Status: t.Status()})
	}
	return items, nil
}

// HTTP 处理 GET /api/tenants。
func (a *Mine) HTTP(ctx context.Context, _ *struct{}) (*MineOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &MineOutput{Body: items}, nil
}
