package auth

import (
	"context"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/identity"
)

// 预置的全局角色：应用启动时由 EnsureRoles 确保存在且与这里的声明一致，代码中改了权限，下一次启动时生效。
var (
	// PlatformAdmin 是平台管理员，分配在平台的域 "" 中。
	PlatformAdmin = authz.Role{
		Name: "platform-admin", Description: "平台管理员",
		Permissions: []string{string(TenantsRead), string(TenantsManage), string(PlatformSettingsManage)},
	}
	// Owner 是租户（工作区）的主管理员，分配在租户的域中。PR 2 定下工作区的权限后改为明确的列表。
	Owner = authz.Role{Name: "owner", Description: "工作区的主管理员", Permissions: []string{"*"}}
)

// EnsureRoles 确保预置的角色存在且与声明一致，在入口的 open 中调用：重复调用、多个实例同时启动都是安全的。
func EnsureRoles(ctx context.Context, roles *authz.Store) error {
	for _, r := range []authz.Role{PlatformAdmin, Owner} {
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
