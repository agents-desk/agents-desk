package role

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册角色，并把角色声明为资源。
// 读取列表和详情要求 members:read（供成员列表筛选器使用），新建、修改和删除要求 members:manage。
// require 返回要求成员登录凭据和给定权限的访问策略。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, roles *authz.Store) {
	read, manage := require(auth.MembersRead), require(auth.MembersManage)
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	create := route(manage, "role-create", http.MethodPost, "/roles", "新建角色")
	create.DefaultStatus = http.StatusCreated
	del := route(manage, "role-delete", http.MethodDelete, "/roles/{id}", "删除角色")
	del.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(read, "role-list", http.MethodGet, "/roles", "角色列表"), NewList(roles).HTTP)
	rest.Register(api, route(read, "role-get", http.MethodGet, "/roles/{id}", "读取角色"), NewGet(roles).HTTP)
	rest.Register(api, create, NewCreate(db, roles).HTTP)
	rest.Register(api, route(manage, "role-update", http.MethodPut, "/roles/{id}", "修改角色"), NewUpdate(db, roles).HTTP)
	rest.Register(api, del, NewDelete(db, roles).HTTP)

	api.Resource(rest.Resource{
		Name:    "role",
		List:    "role-list",
		Get:     "role-get",
		Create:  "role-create",
		Update:  "role-update",
		Delete:  "role-delete",
		Columns: []string{"name", "conversation_scope", "member_count", "description"},
		Filters: []string{"name"},
	})
}
