package role

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/identity"
)

// Get 读取当前工作区的一个角色。
type Get struct {
	roles *authz.Store
}

// NewGet 返回读取角色的动作。
func NewGet(roles *authz.Store) *Get { return &Get{roles: roles} }

// Handle 返回 sub 所在工作区的角色 id，带有人数。角色不存在、或者不是这个工作区自己的角色时返回 ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*RoleDTO, error) {
	r, err := a.roles.Role(ctx, id, sub.Tenant)
	if errors.Is(err, authz.ErrRoleNotFound) {
		return nil, ErrNotFound.New()
	}
	if err != nil {
		return nil, err
	}
	if r.Tenant != sub.Tenant {
		return nil, ErrNotFound.New() // 全局角色，例如主管理员
	}
	counts, err := a.roles.RoleMemberCounts(ctx, sub.Tenant)
	if err != nil {
		return nil, err
	}
	dto := toDTO(r, counts[r.ID])
	return &dto, nil
}

// HTTP 处理 GET /t/{slug}/api/roles/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*RoleOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &RoleOutput{Body: *dto}, nil
}
