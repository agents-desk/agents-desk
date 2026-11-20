package member

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
)

// notOwnerOrSelf 是不是主管理员、也不是自己的行：只有这些行上显示“修改”和“移出”。
var notOwnerOrSelf = map[string][]string{
	"role": {string(auth.RoleAdmin), string(auth.RoleAgent)},
	"self": {"false"},
}

// RegisterTenantRoutes 在工作区的 API（/t/{slug}/api）中注册成员管理，并把成员声明为资源。
// require 返回要求成员登录凭据和给定权限的访问策略；hooks 是移出成员之后其他模块跟着做的事。
func RegisterTenantRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, roles *authz.Store, logins *login.Realm, files *filesystem.Service, hooks Hooks) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	read, manage := require(auth.MembersRead), require(auth.MembersManage)
	add := route(manage, "member-add", http.MethodPost, "/members", "添加成员")
	add.DefaultStatus = http.StatusCreated
	remove := route(manage, "member-remove", http.MethodPost, "/members/{id}/remove", "移出成员")
	remove.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(read, "member-list", http.MethodGet, "/members", "成员列表"), NewList(db, roles, logins, files).HTTP)
	rest.Register(api, route(read, "member-get", http.MethodGet, "/members/{id}", "读取成员"), NewGet(db, roles, logins, files).HTTP)
	rest.Register(api, add, NewAdd(db, logins, files).HTTP)
	rest.Register(api, route(manage, "member-update", http.MethodPut, "/members/{id}", "修改成员的角色"), NewChangeRole(db, logins, files).HTTP)
	rest.Register(api, remove, NewRemove(db, hooks).HTTP)

	api.Resource(rest.Resource{
		Name:       "member",
		List:       "member-list",
		Get:        "member-get",
		Create:     "member-add",
		Update:     "member-update",
		Columns:    []string{"name", "email", "role"},
		Filters:    []string{"name", "role"},
		CreateForm: []string{"email", "name", "password", "role"},
		// 主管理员那一行和自己那一行没有“修改”“移出”：主管理员只在管理员端更换，任何人不能修改、移出自己
		UpdateWhen: notOwnerOrSelf,
		Actions: []rest.Action{
			{Name: "remove", Operation: "member-remove", Confirm: true, Danger: true, When: notOwnerOrSelf},
		},
	})
}
