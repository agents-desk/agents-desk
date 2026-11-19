// Package auth 是应用的权限目录和预置角色。平台和每个租户（工作区）各是一个权限的域，共用这一份目录。
package auth

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
)

// Permission 是一个权限，例如 tenants:manage。
type Permission string

const (
	TenantsRead            Permission = "tenants:read"             // 平台：查看租户
	TenantsManage          Permission = "tenants:manage"           // 平台：创建、修改、停用租户，更换主管理员
	PlatformSettingsManage Permission = "platform-settings:manage" // 平台：修改平台设置

	MembersRead          Permission = "members:read"           // 工作区：查看成员
	MembersManage        Permission = "members:manage"         // 工作区：添加成员、修改角色、移出成员
	TenantSettingsManage Permission = "tenant-settings:manage" // 工作区：修改工作区的常规设置（名称）
)

// Permissions 是全部权限，显示文字是翻译文件中 permission.<权限> 的消息。
var Permissions = enum.New("permission", TenantsRead, TenantsManage, PlatformSettingsManage, MembersRead, MembersManage, TenantSettingsManage)

// Schema 让 OpenAPI 中的 Permission 只接受目录中的取值。
func (Permission) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Permissions) }

// Catalog 是权限目录：authz 只接受其中的权限，它也写入 OpenAPI 文档，生成的客户端据此得到权限常量。
var Catalog = authz.Catalog{
	{Name: string(TenantsRead), Description: "查看工作区"},
	{Name: string(TenantsManage), Description: "管理工作区"},
	{Name: string(PlatformSettingsManage), Description: "修改平台设置"},
	{Name: string(MembersRead), Description: "查看成员"},
	{Name: string(MembersManage), Description: "管理成员"},
	{Name: string(TenantSettingsManage), Description: "修改工作区设置"},
}
