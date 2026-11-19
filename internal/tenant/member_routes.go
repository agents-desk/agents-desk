package tenant

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/httpx/rest"
)

// RegisterMemberRoutes 在成员的账号级 API（/api）中注册“我所属的工作区”。requireMember 要求成员的登录凭据。
func RegisterMemberRoutes(api *rest.API, requireMember func(*huma.Operation), db *bun.DB, roles *authz.Store, addr Addresses) {
	op := huma.Operation{OperationID: "tenant-mine", Method: http.MethodGet, Path: "/tenants", Summary: "我所属的工作区"}
	requireMember(&op)
	rest.Register(api, op, NewMine(db, roles, addr).HTTP)
}

// RegisterTenantRoutes 在工作区的 API（/t/{slug}/api）中注册“当前工作区”。requireMember 要求成员的登录凭据；
// 工作区由 Middleware 和 FromRequest 确定，成员身份和停用由 Resolve 检查。
func RegisterTenantRoutes(api *rest.API, requireMember func(*huma.Operation), db *bun.DB) {
	op := huma.Operation{OperationID: "tenant-current", Method: http.MethodGet, Path: "/tenant", Summary: "当前工作区和我的权限"}
	requireMember(&op)
	rest.Register(api, op, NewCurrent(db).HTTP)
}
