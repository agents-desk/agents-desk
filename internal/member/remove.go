package member

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// Remove 把一个成员移出当前工作区：撤销他在这个工作区的角色。账号、他在其他工作区的角色和他留下的记录都保留。
type Remove struct {
	db    *bun.DB
	hooks Hooks
}

// NewRemove 返回移出成员的动作；hooks.Removed 在同一个事务中跟着执行，例如把他移出这个工作区的团队。
func NewRemove(db *bun.DB, hooks Hooks) *Remove { return &Remove{db: db, hooks: hooks} }

// Handle 在 sub 所在的工作区中移出成员 id，并记录操作日志。不能移出自己（ErrSelf）和主管理员（ErrOwnerProtected）；
// 他不是这个工作区的成员时返回 ErrNotFound。移出后他再访问这个工作区得到 404。
func (a *Remove) Handle(ctx context.Context, sub identity.Subject, id string) error {
	if id == sub.ID {
		return ErrSelf.New()
	}
	ref := identity.Ref{Type: Type, ID: id}
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		roles := authz.NewStore(tx, auth.Catalog)
		current, err := RoleOf(ctx, roles, ref, sub.Tenant)
		if err != nil {
			return err
		}
		if auth.IsOwner(current) {
			return ErrOwnerProtected.New()
		}
		if _, err := roles.RemoveMember(ctx, ref, sub.Tenant); err != nil {
			return err
		}
		if a.hooks.Removed != nil {
			if err := a.hooks.Removed(ctx, tx, sub.Tenant, id); err != nil {
				return err
			}
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.removed", TargetType: string(Type), TargetID: id,
			Properties: map[string]any{"role": current.Name},
		})
	})
}

// HTTP 处理 POST /t/{slug}/api/members/{id}/remove。
func (a *Remove) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
