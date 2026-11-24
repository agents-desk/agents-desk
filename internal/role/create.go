package role

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// CreateInput 是新建角色的 HTTP 请求。
type CreateInput struct {
	Body Params
}

// Create 在当前工作区中新建角色。
type Create struct {
	db    *bun.DB
	roles *authz.Store
}

// NewCreate 返回新建角色的动作。
func NewCreate(db *bun.DB, roles *authz.Store) *Create { return &Create{db: db, roles: roles} }

// Handle 在 sub 所在工作区中新建角色 p 并记录操作日志。授予的权限超出 sub 自身权限时返回 authz.ErrExceedsPermissions，
// 名称已被使用时返回 ErrNameTaken（字段 name）。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, p Params) (*RoleDTO, error) {
	r := toRole(sub.Tenant, p)
	if err := a.roles.CheckGrant(sub, r.Permissions); err != nil {
		return nil, err
	}
	var created authz.Role
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		created, err = a.roles.WithDB(tx).CreateRole(ctx, r)
		if err != nil {
			return nameTaken(err, p.Name)
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "role.created", TargetType: "role", TargetID: created.ID, Properties: map[string]any{"name": p.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	dto := toDTO(created, 0)
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/roles。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*RoleOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &RoleOutput{Body: *dto}, nil
}
