package tenant

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterMemberRoutes 在成员的账号级 API（/api）中注册“我所属的工作区”。requireMember 要求成员的登录凭据。
func RegisterMemberRoutes(api *rest.API, requireMember func(*huma.Operation), db *bun.DB, roles *authz.Store, addr Addresses) {
	op := huma.Operation{OperationID: "tenant-mine", Method: http.MethodGet, Path: "/tenants", Summary: "我所属的工作区"}
	requireMember(&op)
	rest.Register(api, op, NewMine(db, roles, addr).HTTP)
}

// RegisterTenantRoutes 在工作区的 API（/t/{slug}/api）中注册“当前工作区”和修改它的常规设置。require 返回要求成员
// 登录凭据和给定权限的访问策略；工作区由 Middleware 和 FromRequest 确定，成员身份和停用由 Resolve 检查。
func RegisterTenantRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	op := huma.Operation{OperationID: "tenant-current", Method: http.MethodGet, Path: "/tenant", Summary: "当前工作区和我的权限"}
	require()(&op)
	rest.Register(api, op, NewCurrent(db).HTTP)

	op = huma.Operation{OperationID: "tenant-rename", Method: http.MethodPut, Path: "/tenant", Summary: "修改工作区的常规设置"}
	require(auth.TenantSettingsManage)(&op)
	rest.Register(api, op, NewRename(db).HTTP)
}
