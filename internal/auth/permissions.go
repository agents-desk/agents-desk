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
	PlatformAdminsManage   Permission = "platform-admins:manage"   // 平台：查看、创建、修改、停用、启用平台管理员
	PlatformSettingsManage Permission = "platform-settings:manage" // 平台：修改平台设置
	FeaturesManage         Permission = "features:manage"          // 平台：管理功能开关与灰度放量
	QueueManage            Permission = "queue:manage"             // 平台：查看队列状态、重试/删除失败任务、暂停/恢复 topic
	SchedulesManage        Permission = "schedules:manage"         // 平台：查看定时任务与执行记录、立即执行任务
	SystemRead             Permission = "system:read"              // 平台：查看运行环境自检报告
	AuditRead              Permission = "audit:read"               // 平台：查看平台操作日志

	MembersRead           Permission = "members:read"            // 工作区：查看成员
	MembersManage         Permission = "members:manage"          // 工作区：添加成员、修改成员的角色、移出成员，新建、修改、删除角色
	TenantSettingsManage  Permission = "tenant-settings:manage"  // 工作区：修改工作区的常规设置（名称）
	TeamsRead             Permission = "teams:read"              // 工作区：查看团队收件箱和团队收件箱的成员
	TeamsManage           Permission = "teams:manage"            // 工作区：新建、修改、删除团队收件箱，增减团队收件箱的成员
	ChannelsRead          Permission = "channels:read"           // 工作区：查看工作区的渠道列表（名称与类型）
	ChannelsManage        Permission = "channels:manage"         // 工作区：新建、修改、停用渠道，查看渠道的嵌入代码和身份验证密钥
	MacrosManage          Permission = "macros:manage"           // 工作区：新建、修改、删除共享的快捷回复
	ReportsRead           Permission = "reports:read"            // 工作区：查看工作区报表与统计数据
	ConversationsRead     Permission = "conversations:read"      // 工作区：查看会话和时间线
	ConversationsReply    Permission = "conversations:reply"     // 工作区：回复会话、写内部备注，处理看得到的会话（关闭、稍后处理、重新打开、优先级）
	ConversationsAssign   Permission = "conversations:assign"    // 工作区：把会话改派给别人：指派别人负责的会话，或者把没有负责人的会话指派给别人、转到别的团队收件箱
	ConversationsReadAll  Permission = "conversations:read-all"  // 工作区：会话的可见范围是全部会话
	ConversationsReadTeam Permission = "conversations:read-team" // 工作区：会话的可见范围是所在团队收件箱的和指派给自己的
	ContactsRead          Permission = "contacts:read"           // 工作区：查看联系人列表和资料
	ContactsUpdate        Permission = "contacts:update"         // 工作区：修改联系人资料和备注
)

// Permissions 是全部权限，显示文字是翻译文件中 permission.<权限> 的消息。
var Permissions = enum.New("permission", enum.Item(TenantsRead), enum.Item(TenantsManage), enum.Item(PlatformAdminsManage), enum.Item(PlatformSettingsManage),
	enum.Item(FeaturesManage), enum.Item(QueueManage), enum.Item(SchedulesManage), enum.Item(SystemRead), enum.Item(AuditRead),
	enum.Item(MembersRead), enum.Item(MembersManage), enum.Item(TenantSettingsManage), enum.Item(TeamsRead), enum.Item(TeamsManage), enum.Item(ChannelsRead), enum.Item(ChannelsManage), enum.Item(MacrosManage), enum.Item(ReportsRead),
	enum.Item(ConversationsRead), enum.Item(ConversationsReply), enum.Item(ConversationsAssign), enum.Item(ConversationsReadAll), enum.Item(ConversationsReadTeam),
	enum.Item(ContactsRead), enum.Item(ContactsUpdate))

// Schema 让 OpenAPI 中的 Permission 只接受目录中的取值。
func (Permission) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Permissions) }

// Catalog 是权限目录：authz 只接受其中的权限，它也写入 OpenAPI 文档，生成的客户端据此得到权限常量。
var Catalog = authz.Catalog{
	{Name: string(TenantsRead), Description: "查看工作区"},
	{Name: string(TenantsManage), Description: "管理工作区"},
	{Name: string(PlatformAdminsManage), Description: "管理平台管理员"},
	{Name: string(PlatformSettingsManage), Description: "修改平台设置"},
	{Name: string(FeaturesManage), Description: "管理功能开关"},
	{Name: string(QueueManage), Description: "管理队列"},
	{Name: string(SchedulesManage), Description: "管理定时任务"},
	{Name: string(SystemRead), Description: "查看系统自检"},
	{Name: string(AuditRead), Description: "查看操作日志"},
	{Name: string(MembersRead), Description: "查看成员"},
	{Name: string(MembersManage), Description: "管理成员和角色"},
	{Name: string(TenantSettingsManage), Description: "管理工作区设置"},
	{Name: string(TeamsRead), Description: "查看团队收件箱"},
	{Name: string(TeamsManage), Description: "管理团队收件箱"},
	{Name: string(ChannelsRead), Description: "查看渠道"},
	{Name: string(ChannelsManage), Description: "管理渠道"},
	{Name: string(MacrosManage), Description: "管理共享的快捷回复"},
	{Name: string(ReportsRead), Description: "查看报表"},
	{Name: string(ConversationsRead), Description: "查看会话"},
	{Name: string(ConversationsReply), Description: "回复会话"},
	{Name: string(ConversationsAssign), Description: "把会话改派给别人"},
	{Name: string(ConversationsReadAll), Description: "可以看到全部会话"},
	{Name: string(ConversationsReadTeam), Description: "可以看到所在团队收件箱的会话"},
	{Name: string(ContactsRead), Description: "查看联系人"},
	{Name: string(ContactsUpdate), Description: "修改联系人"},
}

// BasePermissions 是工作区每位成员都有的基本权限：查看成员、团队收件箱和渠道、查看和回复会话、查看和修改联系人，
// 能看到哪些会话由可见范围决定。它们不保存在角色中，加载成员的权限时由 WithBase 加上，所以改了这里，所有成员下一个请求起都按新的基本权限检查。
var BasePermissions = []Permission{MembersRead, TeamsRead, ChannelsRead, ConversationsRead, ConversationsReply, ContactsRead, ContactsUpdate}

// WithBase 返回 perms 加上基本权限，去重并排序：成员在工作区中的全部权限。
func WithBase(perms []string) []string {
	all := slices.Clone(perms)
	for _, p := range BasePermissions {
		all = append(all, string(p))
	}
	slices.Sort(all)
	return slices.Compact(all)
}

// TenantPermission 是工作区的角色中可以勾选的操作权限。
type TenantPermission string

// TenantPermissions 是可以勾选的操作权限，显示文字是翻译文件中 role_permission.<权限> 的消息。前缀与 Permissions 不同：
// 枚举在接口文档中按前缀命名，同一个前缀的两个枚举会被当成同一个类型，平台的权限就成了可以勾选的权限。
var TenantPermissions = enum.New("role_permission",
	enum.Item(TenantPermission(ConversationsAssign)), enum.Item(TenantPermission(MacrosManage)), enum.Item(TenantPermission(ReportsRead)), enum.Item(TenantPermission(MembersManage)), enum.Item(TenantPermission(TeamsManage)), enum.Item(TenantPermission(ChannelsManage)), enum.Item(TenantPermission(TenantSettingsManage)))

// Schema 让 OpenAPI 中的 TenantPermission 只接受上面的取值，并带有显示文字。
func (TenantPermission) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, TenantPermissions)
}

// ConversationScope 是角色的会话可见范围，三选一。
type ConversationScope string

const (
	ScopeAll      ConversationScope = "all"      // 全部会话
	ScopeTeam     ConversationScope = "team"     // 自己所在团队收件箱的和指派给自己的
	ScopeAssigned ConversationScope = "assigned" // 只有指派给自己的
)

// ConversationScopes 是全部可见范围，显示文字是翻译文件中 conversation_scope.<范围> 的消息。
var ConversationScopes = enum.New("conversation_scope", enum.Item(ScopeAll), enum.Item(ScopeTeam), enum.Item(ScopeAssigned))

// Schema 让 OpenAPI 中的 ConversationScope 只接受上面的取值，并带有显示文字。
func (ConversationScope) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, ConversationScopes)
}

// scopePermissions 是可见范围在角色中对应的权限；只看指派给自己的会话不需要权限。
var scopePermissions = map[ConversationScope]Permission{ScopeAll: ConversationsReadAll, ScopeTeam: ConversationsReadTeam}

// ScopeOf 返回 sub 的会话可见范围：有 conversations:read-all 时是全部，有 conversations:read-team 时是所在团队收件箱的，
// 否则只有指派给自己的。
func ScopeOf(sub identity.Subject) ConversationScope {
	switch {
	case sub.Can(string(ConversationsReadAll)):
		return ScopeAll
	case sub.Can(string(ConversationsReadTeam)):
		return ScopeTeam
	}
	return ScopeAssigned
}

// RolePermissions 返回工作区的角色保存在 authz 中的权限：可见范围对应的权限和勾选的操作权限，去重并排序。
func RolePermissions(scope ConversationScope, ops []TenantPermission) []string {
	perms := make([]string, 0, 1+len(ops))
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
	if slices.Contains(perms, string(ConversationsReadTeam)) {
		scope = ScopeTeam
	}
	if slices.Contains(perms, string(ConversationsReadAll)) {
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
