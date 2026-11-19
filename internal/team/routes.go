package team

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

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册团队和团队的成员，并把团队声明为资源。
// require 返回要求成员登录凭据和给定权限的访问策略；logins、files 用于显示团队成员的邮箱和头像。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, roles *authz.Store, logins *login.Realm, files *filesystem.Service) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	read, manage := require(auth.TeamsRead), require(auth.TeamsManage)
	create := route(manage, "team-create", http.MethodPost, "/teams", "新建团队")
	create.DefaultStatus = http.StatusCreated
	del := route(manage, "team-delete", http.MethodDelete, "/teams/{id}", "删除团队")
	del.DefaultStatus = http.StatusNoContent
	removeMember := route(manage, "team-member-remove", http.MethodDelete, "/teams/{id}/members/{member_id}", "移出团队")
	removeMember.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(read, "team-list", http.MethodGet, "/teams", "团队列表"), NewList(db).HTTP)
	rest.Register(api, route(read, "team-get", http.MethodGet, "/teams/{id}", "读取团队"), NewGet(db).HTTP)
	rest.Register(api, create, NewCreate(db).HTTP)
	rest.Register(api, route(manage, "team-update", http.MethodPut, "/teams/{id}", "修改团队"), NewUpdate(db).HTTP)
	rest.Register(api, del, NewDelete(db).HTTP)
	rest.Register(api, route(read, "team-members", http.MethodGet, "/teams/{id}/members", "团队的成员"), NewListMembers(db, roles, logins, files).HTTP)
	rest.Register(api, route(manage, "team-member-put", http.MethodPut, "/teams/{id}/members/{member_id}", "加入团队或修改组长身份"), NewPutMember(db, logins, files).HTTP)
	rest.Register(api, removeMember, NewRemoveMember(db).HTTP)

	api.Resource(rest.Resource{
		Name:    "team",
		List:    "team-list",
		Get:     "team-get",
		Create:  "team-create",
		Update:  "team-update",
		Delete:  "team-delete",
		Columns: []string{"name", "member_count", "leads", "description"},
		Filters: []string{"name"},
	})
}
