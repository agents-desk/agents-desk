package member

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/account"
	"agents-desk/internal/auth"
)

// 工作区中的成员管理：成员就是在这个工作区的域中有角色的账号，每人一个角色。角色是工作区自己的角色（internal/role），
// 或者预置的主管理员（auth.Owner）。主管理员只在管理员端更换，
// 工作区端不能把人设成主管理员，也不能修改或移出主管理员；任何人都不能修改自己的角色、不能把自己移出。
var (
	ErrAlreadyMember  = errorx.Define(errorx.Conflict, "member.already_member")
	ErrOwnerProtected = errorx.Define(errorx.PermissionDenied, "member.owner_protected")
	ErrSelf           = errorx.Define(errorx.Conflict, "member.self")
)

// TenantMember 是工作区中的一个成员：账号和他在这个工作区的角色。
type TenantMember struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url" doc:"头像的地址；没有头像时为空"`
	RoleID    string `json:"role_id" format:"uuid" doc:"角色的 ID"`
	RoleName  string `json:"role_name" doc:"角色的名称"`
	Owner     bool   `json:"owner" doc:"是不是主管理员：主管理员那一行不能修改角色、不能移出"`
	Status    Status `json:"status" doc:"在这个工作区中的在线状态"`
	Self      bool   `json:"self" doc:"是不是当前登录的成员自己：自己那一行不能修改角色、不能移出"`
}

// RemovedHook 是成员 memberID 离开工作区 tenant 之后，其他模块在同一个事务 tx 中跟着做的一件事。
type RemovedHook func(ctx context.Context, tx bun.IDB, tenant, memberID string) error

// Hooks 是成员被移出工作区之后，其他模块在同一个事务中跟着做的事，由入口传入，例如把他移出这个工作区的团队收件箱。
type Hooks struct {
	Removed []RemovedHook // 离开工作区之后，按顺序执行
}

// TenantMemberOutput 是返回一个成员的响应。
type TenantMemberOutput struct {
	Body TenantMember
}

// RoleName 返回角色 r 给人看的名称：工作区自己的角色是它的名称，主管理员是翻译文件中 role.owner 的消息。
func RoleName(ctx context.Context, r authz.Role) string {
	if auth.IsOwner(r) {
		return i18n.T(ctx, "role.owner", nil)
	}
	return r.Name
}

// RoleOf 返回 ref 在 tenant 中的角色：每位成员只有一个角色。不是这个工作区的成员时返回 ErrNotFound。
// roles 可以建在业务事务上。
func RoleOf(ctx context.Context, roles *authz.Store, ref identity.Ref, tenant string) (authz.Role, error) {
	assigned, err := roles.RolesOf(ctx, ref, tenant)
	if err != nil {
		return authz.Role{}, err
	}
	if len(assigned) == 0 {
		return authz.Role{}, ErrNotFound.New()
	}
	return assigned[0], nil
}

// toTenantMember 把账号 m、他的角色和在线状态转换为接口中的成员：读取登录邮箱和头像的地址；sub 是当前登录的成员。
func toTenantMember(ctx context.Context, logins *login.Realm, files *filesystem.Service, sub identity.Subject, m *Member, role authz.Role, status Status) (TenantMember, error) {
	email, err := Email(ctx, logins, m.Ref())
	if err != nil {
		return TenantMember{}, err
	}
	avatar, err := account.AvatarURL(ctx, files, m.AvatarFileID)
	if err != nil {
		return TenantMember{}, err
	}
	return TenantMember{ID: m.ID, Name: m.Name, Email: email, AvatarURL: avatar,
		RoleID: role.ID, RoleName: RoleName(ctx, role), Owner: auth.IsOwner(role), Status: status, Self: m.ID == sub.ID}, nil
}
