// Package auth 是应用的权限目录、会话的可见范围和预置角色。平台和每个租户（工作区）各是一个权限的域，共用这一份目录。
package auth

import (
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/identity"
)

// Permission 是一个权限，例如 tenants:manage。
type Permission string

const (
	TenantsRead            Permission = "tenants:read"             // 平台：查看租户
	TenantsManage          Permission = "tenants:manage"           // 平台：创建、修改、停用租户，更换主管理员
	PlatformSettingsManage Permission = "platform-settings:manage" // 平台：修改平台设置

	MembersRead           Permission = "members:read"            // 工作区：查看成员
	MembersManage         Permission = "members:manage"          // 工作区：添加成员、修改成员的角色、移出成员，新建、修改、删除角色
	TenantSettingsManage  Permission = "tenant-settings:manage"  // 工作区：修改工作区的常规设置（名称）
	TeamsRead             Permission = "teams:read"              // 工作区：查看团队和团队的成员
	TeamsManage           Permission = "teams:manage"            // 工作区：新建、修改、删除团队，增减团队的成员
	ConversationsRead     Permission = "conversations:read"      // 工作区：查看会话和时间线
	ConversationsReply    Permission = "conversations:reply"     // 工作区：回复会话、写内部备注
	ConversationsViewAll  Permission = "conversations:view-all"  // 工作区：会话的可见范围是全部会话
	ConversationsViewTeam Permission = "conversations:view-team" // 工作区：会话的可见范围是所在团队的和指派给自己的
)

// Permissions 是全部权限，显示文字是翻译文件中 permission.<权限> 的消息。
var Permissions = enum.New("permission", TenantsRead, TenantsManage, PlatformSettingsManage,
	MembersRead, MembersManage, TenantSettingsManage, TeamsRead, TeamsManage,
	ConversationsRead, ConversationsReply, ConversationsViewAll, ConversationsViewTeam)

// Schema 让 OpenAPI 中的 Permission 只接受目录中的取值。
func (Permission) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Permissions) }

// Catalog 是权限目录：authz 只接受其中的权限，它也写入 OpenAPI 文档，生成的客户端据此得到权限常量。
var Catalog = authz.Catalog{
	{Name: string(TenantsRead), Description: "查看工作区"},
	{Name: string(TenantsManage), Description: "管理工作区"},
	{Name: string(PlatformSettingsManage), Description: "修改平台设置"},
	{Name: string(MembersRead), Description: "查看成员"},
	{Name: string(MembersManage), Description: "管理成员和角色"},
	{Name: string(TenantSettingsManage), Description: "管理工作区设置"},
	{Name: string(TeamsRead), Description: "查看团队"},
	{Name: string(TeamsManage), Description: "管理团队"},
	{Name: string(ConversationsRead), Description: "查看会话"},
	{Name: string(ConversationsReply), Description: "回复会话"},
	{Name: string(ConversationsViewAll), Description: "可以看到全部会话"},
	{Name: string(ConversationsViewTeam), Description: "可以看到所在团队的会话"},
}

// BasePermissions 是工作区每个角色都有的基本权限：查看成员和团队、查看和回复会话。能看到哪些会话由可见范围决定。
// 角色的编辑界面不列出它们，保存角色时自动加上。
var BasePermissions = []Permission{MembersRead, TeamsRead, ConversationsRead, ConversationsReply}

// TenantPermission 是工作区的角色中可以勾选的操作权限。
type TenantPermission string

// TenantPermissions 是可以勾选的操作权限，显示文字是翻译文件中 role_permission.<权限> 的消息。前缀与 Permissions 不同：
// 枚举在接口文档中按前缀命名，同一个前缀的两个枚举会被当成同一个类型，平台的权限就成了可以勾选的权限。
var TenantPermissions = enum.New("role_permission",
	TenantPermission(MembersManage), TenantPermission(TeamsManage), TenantPermission(TenantSettingsManage))

// Schema 让 OpenAPI 中的 TenantPermission 只接受上面的取值，并带有显示文字。
func (TenantPermission) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, TenantPermissions)
}

// ConversationScope 是角色的会话可见范围，三选一。
type ConversationScope string

const (
	ScopeAll      ConversationScope = "all"      // 全部会话
	ScopeTeam     ConversationScope = "team"     // 自己所在团队的和指派给自己的
	ScopeAssigned ConversationScope = "assigned" // 只有指派给自己的
)

// ConversationScopes 是全部可见范围，显示文字是翻译文件中 conversation_scope.<范围> 的消息。
var ConversationScopes = enum.New("conversation_scope", ScopeAll, ScopeTeam, ScopeAssigned)

// Schema 让 OpenAPI 中的 ConversationScope 只接受上面的取值，并带有显示文字。
func (ConversationScope) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, ConversationScopes)
}

// scopePermissions 是可见范围在角色中对应的权限；只看指派给自己的会话不需要权限。
var scopePermissions = map[ConversationScope]Permission{ScopeAll: ConversationsViewAll, ScopeTeam: ConversationsViewTeam}

// ScopeOf 返回 sub 的会话可见范围：有 conversations:view-all 时是全部，有 conversations:view-team 时是所在团队的，
// 否则只有指派给自己的。
func ScopeOf(sub identity.Subject) ConversationScope {
	switch {
	case sub.Can(string(ConversationsViewAll)):
		return ScopeAll
	case sub.Can(string(ConversationsViewTeam)):
		return ScopeTeam
	}
	return ScopeAssigned
}

// RolePermissions 返回工作区的角色保存在 authz 中的权限：基本权限、可见范围对应的权限和勾选的操作权限，去重并排序。
func RolePermissions(scope ConversationScope, ops []TenantPermission) []string {
	perms := make([]string, 0, len(BasePermissions)+1+len(ops))
	for _, p := range BasePermissions {
		perms = append(perms, string(p))
	}
	if p, ok := scopePermissions[scope]; ok {
		perms = append(perms, string(p))
	}
	for _, p := range ops {
		perms = append(perms, string(p))
	}
	slices.Sort(perms)
	return slices.Compact(perms)
}

// SplitPermissions 是 RolePermissions 的反向：从角色在 authz 中的权限取出可见范围和勾选的操作权限，操作权限按目录的顺序排列。
func SplitPermissions(perms []string) (ConversationScope, []TenantPermission) {
	scope := ScopeAssigned
	if slices.Contains(perms, string(ConversationsViewTeam)) {
		scope = ScopeTeam
	}
	if slices.Contains(perms, string(ConversationsViewAll)) {
		scope = ScopeAll
	}
	ops := []TenantPermission{}
	for _, p := range TenantPermissions.Values() {
		if slices.Contains(perms, string(p)) {
			ops = append(ops, p)
		}
	}
	return scope, ops
}
