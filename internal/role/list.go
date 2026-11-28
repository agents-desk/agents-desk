package role

import (
	"context"
	"slices"
	"strings"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
)

// ListInput 是角色列表的查询条件。
type ListInput struct {
	page.OffsetParams
	Name string `query:"name" maxLength:"30" doc:"名称包含的文字"`
}

// ListOutput 是一页角色。
type ListOutput struct {
	Body page.Offset[RoleDTO]
}

// List 列出当前工作区自己的角色。
type List struct {
	roles *authz.Store
}

// NewList 返回角色列表的动作。
func NewList(roles *authz.Store) *List { return &List{roles: roles} }

// Handle 返回 sub 所在工作区自己的角色，按名称排列，带有人数。主管理员等全局角色不在其中。
// 一个工作区的角色不多，筛选和分页在内存中完成。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[RoleDTO], error) {
	all, err := a.roles.Roles(ctx, sub.Tenant)
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	all = slices.DeleteFunc(all, func(r authz.Role) bool {
		return r.Tenant != sub.Tenant || !strings.Contains(strings.ToLower(r.Name), name)
	})
	counts, err := a.roles.RoleMemberCounts(ctx, sub.Tenant)
	if err != nil {
		return nil, err
	}
	total := int64(len(all))
	all = all[min(in.Offset(), total):min(in.Offset()+in.Limit(), total)]
	items := make([]RoleDTO, len(all))
	for i, r := range all {
		items[i] = toDTO(r, counts[r.ID])
	}
	p := page.NewOffset(items, in.OffsetParams, total)
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/roles。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
