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
	"owner": {"false"},
	"self":  {"false"},
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
	away := route(manage, "member-set-away", http.MethodPost, "/members/{id}/away", "把成员设为离开")
	away.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(read, "member-list", http.MethodGet, "/members", "成员列表"), NewList(db, roles, logins, files).HTTP)
	rest.Register(api, route(read, "member-get", http.MethodGet, "/members/{id}", "读取成员"), NewGet(db, roles, logins, files).HTTP)
	rest.Register(api, add, NewAdd(db, logins, files).HTTP)
	rest.Register(api, route(manage, "member-update", http.MethodPut, "/members/{id}", "修改成员的角色"), NewChangeRole(db, logins, files).HTTP)
	rest.Register(api, remove, NewRemove(db, hooks).HTTP)
	rest.Register(api, away, NewSetAway(db).HTTP)
	rest.Register(api, route(require(), "member-my-status", http.MethodGet, "/me/status", "我在这个工作区的在线状态"), NewGetMyStatus(db).HTTP)
	rest.Register(api, route(require(), "member-my-status-update", http.MethodPut, "/me/status", "修改我在这个工作区的在线状态"), NewSetMyStatus(db).HTTP)

	api.Resource(rest.Resource{
		Name:       "member",
		List:       "member-list",
		Get:        "member-get",
		Create:     "member-add",
		Update:     "member-update",
		Columns:    []string{"name", "email", "role_name", "status"},
		Filters:    []string{"name", "role_id"},
		CreateForm: []string{"email", "name", "password", "role_id"},
		Refs:       map[string]string{"role_id": "role"},
		// 主管理员那一行和自己那一行没有“修改”“移出”：主管理员只在管理员端更换，任何人不能修改、移出自己
		UpdateWhen: notOwnerOrSelf,
		Actions: []rest.Action{
			// 别人在线时可以把他设为离开，例如他下班时忘了切换
			{Name: "away", Operation: "member-set-away", Confirm: true, When: map[string][]string{"self": {"false"}, "status": {string(Online)}}},
			{Name: "remove", Operation: "member-remove", Confirm: true, Danger: true, When: notOwnerOrSelf},
		},
	})
}
