package role

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// UpdateInput 是修改角色的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body Params
}

// Update 修改当前工作区的一个角色，提交后立即通知拥有该角色的在线长连接复查权限。
type Update struct {
	db    *bun.DB
	roles *authz.Store
}

// NewUpdate 返回修改角色的动作。
func NewUpdate(db *bun.DB, roles *authz.Store) *Update { return &Update{db: db, roles: roles} }

// Handle 把 sub 所在工作区的角色 id 改成 p 并记录操作日志。授予的权限超出 sub 自身权限时返回 authz.ErrExceedsPermissions；
// 角色不存在、或者不是这个工作区自己的角色时返回 ErrNotFound；名称已被使用时返回 ErrNameTaken（字段 name）。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, p Params) (*RoleDTO, error) {
	r := toRole(sub.Tenant, p)
	r.ID = id
	if err := a.roles.CheckGrant(sub, r.Permissions); err != nil {
		return nil, err
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := a.roles.WithDB(tx).UpdateRole(ctx, r, sub.Tenant); err != nil {
			if errors.Is(err, authz.ErrRoleNotFound) {
				return ErrNotFound.New()
			}
			return nameTaken(err, p.Name)
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "role.updated", TargetType: "role", TargetID: id, Properties: map[string]any{"name": p.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewGet(a.roles).Handle(ctx, sub, id)
}

// HTTP 处理 PUT /t/{slug}/api/roles/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*RoleOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &RoleOutput{Body: *dto}, nil
}
