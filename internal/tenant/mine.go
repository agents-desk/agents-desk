package tenant

import (
	"context"
	"slices"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
)

// MineItem 是成员所属的一个工作区。已停用的也列出，界面中显示为已停用，不能进入。
type MineItem struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	LogoURL string `json:"logo_url" doc:"工作区 Logo 公开地址，未设置时为空"`
	URL     string `json:"url" doc:"成员进入工作区的地址"`
	Status  Status `json:"status"`
}

// MineOutput 是成员所属的全部工作区。
type MineOutput struct {
	Body []MineItem
}

// Mine 列出成员所属的工作区：登录后的去处和切换工作区的菜单用它。
type Mine struct {
	db    *bun.DB
	roles *authz.Store
	files *filesystem.Service
	addr  Addresses
}

// NewMine 返回列出成员所属工作区的动作。
func NewMine(db *bun.DB, roles *authz.Store, files *filesystem.Service, addr Addresses) *Mine {
	return &Mine{db: db, roles: roles, files: files, addr: addr}
}

// Handle 返回 sub 有角色的全部工作区，按名称排序。
// 凭据绑定了单个租户（sub.BoundTenant 非空，如代为登录）时，只返回这个工作区，不暴露成员所属的其他工作区。
func (a *Mine) Handle(ctx context.Context, sub identity.Subject) ([]MineItem, error) {
	ids, err := a.roles.Tenants(ctx, sub.Ref())
	if err != nil {
		return nil, err
	}
	if sub.BoundTenant != "" {
		if slices.Contains(ids, sub.BoundTenant) {
			ids = []string{sub.BoundTenant}
		} else {
			ids = nil
		}
	}
	items := []MineItem{}
	if len(ids) == 0 {
		return items, nil
	}
	var tenants []Tenant
	if err := a.db.NewSelect().Model(&tenants).Where("id IN (?)", bun.In(ids)).Order("name", "slug").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list member tenants")
	}
	var fileIDs []uuid.UUID
	for _, t := range tenants {
		if t.LogoFileID != nil {
			fileIDs = append(fileIDs, *t.LogoFileID)
		}
	}
	var logos map[uuid.UUID]filesystem.File
	if len(fileIDs) > 0 {
		logos, err = a.files.GetMany(ctx, fileIDs)
		if err != nil {
			return nil, err
		}
	}
	for _, t := range tenants {
		var logoURL string
		if t.LogoFileID != nil {
			logoURL = logos[*t.LogoFileID].URL
		}
		items = append(items, MineItem{
			Slug: t.Slug, Name: t.Name, LogoURL: logoURL, URL: a.addr.URL(&t), Status: t.Status(),
		})
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
