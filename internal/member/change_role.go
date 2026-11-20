package member

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/role"
)

// RoleChange 是修改角色的请求。
type RoleChange struct {
	RoleID string `json:"role_id" format:"uuid" doc:"新的角色：这个工作区自己的角色"`
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

// Handle 在 sub 所在的工作区中把成员 id 的角色换成 roleID，并记录操作日志。不能修改自己（ErrSelf）和主管理员
// （ErrOwnerProtected）；他不是这个工作区的成员时返回 ErrNotFound；roleID 不是这个工作区自己的角色时返回
// role.ErrNotFound（字段 role_id）。
func (a *ChangeRole) Handle(ctx context.Context, sub identity.Subject, id, roleID string) (*TenantMember, error) {
	if id == sub.ID {
		return nil, ErrSelf.New()
	}
	ref := identity.Ref{Type: Type, ID: id}
	var next authz.Role
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		roles := authz.NewStore(tx, auth.Catalog)
		current, err := RoleOf(ctx, roles, ref, sub.Tenant)
		if err != nil {
			return err
		}
		if auth.IsOwner(current) {
			return ErrOwnerProtected.New()
		}
		if next, err = role.Lock(ctx, tx, sub.Tenant, roleID, errorx.Field("role_id")); err != nil {
			return err
		}
		if current.ID == next.ID {
			return nil
		}
		if err := roles.Unassign(ctx, ref, sub.Tenant, current.ID); err != nil {
			return err
		}
		if err := roles.Assign(ctx, ref, sub.Tenant, next.ID); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.role_changed", TargetType: string(Type), TargetID: id,
			Properties: map[string]any{"from": current.Name, "to": next.Name, "from_id": current.ID, "to_id": next.ID},
		})
	})
	if err != nil {
		return nil, err
	}
	m, err := Find(ctx, a.db, id)
	if err != nil {
		return nil, err
	}
	status, err := statusOf(ctx, a.db, sub.Tenant, id, false)
	if err != nil {
		return nil, err
	}
	item, err := toTenantMember(ctx, a.logins, a.files, sub, m, next, status.Status)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// HTTP 处理 PUT /t/{slug}/api/members/{id}。
func (a *ChangeRole) HTTP(ctx context.Context, in *ChangeRoleInput) (*TenantMemberOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body.RoleID)
	if err != nil {
		return nil, err
	}
	return &TenantMemberOutput{Body: *item}, nil
}
