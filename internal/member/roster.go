package member

import (
	"context"
	"slices"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/account"
	"agents-desk/internal/auth"
)

// 工作区中的成员管理：成员就是在这个工作区的域中有角色的账号，每人一个角色。主管理员只在管理员端更换，
// 工作区端不能把人设成主管理员，也不能修改或移出主管理员；任何人都不能修改自己的角色、不能把自己移出。
var (
	ErrAlreadyMember  = errorx.Define(errorx.Conflict, "member.already_member")
	ErrOwnerProtected = errorx.Define(errorx.PermissionDenied, "member.owner_protected")
	ErrSelf           = errorx.Define(errorx.Conflict, "member.self")
)

// TenantMember 是工作区中的一个成员：账号和他在这个工作区的角色。
type TenantMember struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Email     string        `json:"email"`
	AvatarURL string        `json:"avatar_url" doc:"头像的地址；没有头像时为空"`
	Role      auth.RoleName `json:"role"`
	Self      bool          `json:"self" doc:"是不是当前登录的成员自己：自己那一行不能修改角色、不能移出"`
}

// Hooks 是成员被移出工作区之后，其他模块在同一个事务中跟着做的事，由入口传入，例如把他移出这个工作区的团队。
type Hooks struct {
	Removed func(ctx context.Context, tx bun.IDB, tenant, memberID string) error // 移出工作区之后
}

// TenantMemberOutput 是返回一个成员的响应。
type TenantMemberOutput struct {
	Body TenantMember
}

// roleRank 是角色从高到低的顺序：列表按它排列，一个人有多个角色时取最高的。
func roleRank(r auth.RoleName) int { return slices.Index(auth.RoleNames.Values(), r) }

// roleIn 从 ref 在 tenant 中的角色里取最高的预置角色。ref 不是这个工作区的成员时 ok 为 false。
func roleIn(roles []authz.Role) (role auth.RoleName, ok bool) {
	for _, r := range roles {
		name := auth.RoleName(r.Name)
		if _, known := auth.TenantRoles[name]; known && (!ok || roleRank(name) < roleRank(role)) {
			role, ok = name, true
		}
	}
	return role, ok
}

// RoleOf 返回 ref 在 tenant 中的角色。不是这个工作区的成员时返回 ErrNotFound。roles 可以建在业务事务上。
func RoleOf(ctx context.Context, roles *authz.Store, ref identity.Ref, tenant string) (auth.RoleName, error) {
	assigned, err := roles.RolesOf(ctx, ref, tenant)
	if err != nil {
		return "", err
	}
	role, ok := roleIn(assigned)
	if !ok {
		return "", ErrNotFound.New()
	}
	return role, nil
}

// toTenantMember 把账号 m 和他的角色转换为接口中的成员：读取登录邮箱和头像的地址；sub 是当前登录的成员。
func toTenantMember(ctx context.Context, logins *login.Realm, files *filesystem.Service, sub identity.Subject, m *Member, role auth.RoleName) (TenantMember, error) {
	email, err := Email(ctx, logins, m.Ref())
	if err != nil {
		return TenantMember{}, err
	}
	avatar, err := account.AvatarURL(ctx, files, m.AvatarFileID)
	if err != nil {
		return TenantMember{}, err
	}
	return TenantMember{ID: m.ID, Name: m.Name, Email: email, AvatarURL: avatar, Role: role, Self: m.ID == sub.ID}, nil
}
