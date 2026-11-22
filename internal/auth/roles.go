package auth

import (
	"context"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/identity"
)

// 预置的全局角色：应用启动时由 EnsureRoles 确保存在且与这里的声明一致，代码中改了权限，下一次启动时生效。
// 工作区的其他角色由工作区自己创建，保存在 authz 中，见 internal/role。
var (
	// PlatformAdmin 是平台管理员，分配在平台的域 "" 中。
	PlatformAdmin = authz.Role{
		Name: "platform-admin", Description: "平台管理员",
		Permissions: []string{
			string(TenantsRead), string(TenantsManage), string(PlatformAdminsManage), string(PlatformSettingsManage),
			string(FeaturesManage), string(QueueManage), string(SchedulesManage), string(SystemRead), string(AuditRead),
		},
	}
	// Owner 是工作区的主管理员，分配在工作区的域中：每个工作区只有一个，只在管理员端更换。
	// 他拥有工作区的全部权限，可以看到全部会话；以后加入的操作权限也自动属于他。
	Owner = authz.Role{
		Name: "owner", Description: "工作区的主管理员",
		Permissions: RolePermissions(ScopeAll, TenantPermissions.Values()),
	}
)

// IsOwner 报告角色 r 是不是主管理员。
func IsOwner(r authz.Role) bool { return r.Tenant == "" && r.Name == Owner.Name }

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
