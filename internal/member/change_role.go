package member

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
)

// RoleChange 是修改角色的请求。
type RoleChange struct {
	Role auth.AssignableRole `json:"role"`
}

// ChangeRoleInput 是修改角色的 HTTP 请求。
type ChangeRoleInput struct {
	ID   string `path:"id" format:"uuid"`
	Body RoleChange
}

// ChangeRole 修改当前工作区中一个成员的角色。
type ChangeRole struct {
	db     *bun.DB
	logins *login.Realm
	files  *filesystem.Service
}

// NewChangeRole 返回修改角色的动作。
func NewChangeRole(db *bun.DB, logins *login.Realm, files *filesystem.Service) *ChangeRole {
	return &ChangeRole{db: db, logins: logins, files: files}
}

// Handle 在 sub 所在的工作区中把成员 id 的角色换成 role，并记录操作日志。不能修改自己（ErrSelf）和主管理员
// （ErrOwnerProtected）；他不是这个工作区的成员时返回 ErrNotFound。
func (a *ChangeRole) Handle(ctx context.Context, sub identity.Subject, id string, role auth.AssignableRole) (*TenantMember, error) {
	if id == sub.ID {
		return nil, ErrSelf.New()
	}
	ref := identity.Ref{Type: Type, ID: id}
	next := auth.RoleName(role)
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		roles := authz.NewStore(tx, auth.Catalog)
		current, err := RoleOf(ctx, roles, ref, sub.Tenant)
		if err != nil {
			return err
		}
		if current == auth.RoleOwner {
			return ErrOwnerProtected.New()
		}
		if current == next {
			return nil
		}
		if err := auth.Revoke(ctx, roles, ref, sub.Tenant, auth.TenantRoles[current]); err != nil {
			return err
		}
		if err := auth.Grant(ctx, roles, ref, sub.Tenant, auth.TenantRoles[next]); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.role_changed", TargetType: string(Type), TargetID: id,
			Properties: map[string]any{"from": current, "to": next},
		})
	})
	if err != nil {
		return nil, err
	}
	m, err := Find(ctx, a.db, id)
	if err != nil {
		return nil, err
	}
	item, err := toTenantMember(ctx, a.logins, a.files, sub, m, next)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// HTTP 处理 PUT /t/{slug}/api/members/{id}。
func (a *ChangeRole) HTTP(ctx context.Context, in *ChangeRoleInput) (*TenantMemberOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body.Role)
	if err != nil {
		return nil, err
	}
	return &TenantMemberOutput{Body: *item}, nil
}
