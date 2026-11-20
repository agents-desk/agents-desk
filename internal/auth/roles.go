package auth

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/identity"
)

// 预置的全局角色：应用启动时由 EnsureRoles 确保存在且与这里的声明一致，代码中改了权限，下一次启动时生效。
var (
	// PlatformAdmin 是平台管理员，分配在平台的域 "" 中。
	PlatformAdmin = authz.Role{
		Name: "platform-admin", Description: "平台管理员",
		Permissions: []string{string(TenantsRead), string(TenantsManage), string(PlatformSettingsManage)},
	}
	// Owner 是租户（工作区）的主管理员，分配在租户的域中。每个工作区只有一个，只在管理员端更换。
	Owner = authz.Role{
		Name: string(RoleOwner), Description: "工作区的主管理员",
		Permissions: []string{string(MembersRead), string(MembersManage), string(TenantSettingsManage), string(TeamsRead), string(TeamsManage)},
	}
	// Admin 是工作区的管理员：管理成员和工作区的设置，不能动主管理员。
	Admin = authz.Role{
		Name: string(RoleAdmin), Description: "工作区的管理员",
		Permissions: []string{string(MembersRead), string(MembersManage), string(TenantSettingsManage), string(TeamsRead), string(TeamsManage)},
	}
	// Agent 是坐席：处理会话。
	Agent = authz.Role{Name: string(RoleAgent), Description: "坐席", Permissions: []string{string(MembersRead), string(TeamsRead)}}
)

// RoleName 是成员在工作区中的角色。每个成员在一个工作区中只有一个角色。
type RoleName string

const (
	RoleOwner RoleName = "owner" // 主管理员
	RoleAdmin RoleName = "admin" // 管理员
	RoleAgent RoleName = "agent" // 坐席
)

// RoleNames 是工作区的全部角色，按从高到低的顺序；显示文字是翻译文件中 role.<角色> 的消息。
var RoleNames = enum.New("role", RoleOwner, RoleAdmin, RoleAgent)

// Schema 让 OpenAPI 中的 RoleName 只接受上面的取值，并带有显示文字。
func (RoleName) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, RoleNames) }

// AssignableRole 是在工作区端可以分配的角色：主管理员只在管理员端更换，不在其中。
type AssignableRole string

// AssignableRoles 是可以分配的角色，显示文字是翻译文件中 assignable_role.<角色> 的消息。前缀与 RoleNames 不同：
// 枚举在接口文档中按前缀命名，同一个前缀的两个枚举会被当成同一个类型，主管理员就成了可以分配的角色。
var AssignableRoles = enum.New("assignable_role", AssignableRole(RoleAdmin), AssignableRole(RoleAgent))

// Schema 让 OpenAPI 中的 AssignableRole 只接受上面的取值，并带有显示文字。
func (AssignableRole) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, AssignableRoles)
}

// TenantRoles 是工作区的预置角色，按 RoleName 查找。
var TenantRoles = map[RoleName]authz.Role{RoleOwner: Owner, RoleAdmin: Admin, RoleAgent: Agent}

// EnsureRoles 确保预置的角色存在且与声明一致，在入口的 open 中调用：重复调用、多个实例同时启动都是安全的。
func EnsureRoles(ctx context.Context, roles *authz.Store) error {
	for _, r := range []authz.Role{PlatformAdmin, Owner, Admin, Agent} {
		if _, err := roles.EnsureRole(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// Grant 在 tenant 中把预置角色 role 分配给 ref。roles 可以建在业务事务上，与业务数据一起提交。
func Grant(ctx context.Context, roles *authz.Store, ref identity.Ref, tenant string, role authz.Role) error {
	r, err := roles.RoleByName(ctx, role.Name, "")
	if err != nil {
		return err
	}
	return roles.Assign(ctx, ref, tenant, r.ID)
}

// Revoke 在 tenant 中撤销 ref 的预置角色 role。
func Revoke(ctx context.Context, roles *authz.Store, ref identity.Ref, tenant string, role authz.Role) error {
	r, err := roles.RoleByName(ctx, role.Name, "")
	if err != nil {
		return err
	}
	return roles.Unassign(ctx, ref, tenant, r.ID)
}
