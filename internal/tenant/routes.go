package tenant

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/login"
)

// RegisterRoutes 注册平台管理员管理租户的接口，并把租户声明为管理员端的资源。
// requireRead 要求 tenants:read，requireManage 要求 tenants:manage；logins 是成员的 login.Realm。
func RegisterRoutes(api *rest.API, requireRead, requireManage func(*huma.Operation), db *bun.DB, logins *login.Realm, addr Addresses) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	create := route(requireManage, "tenant-create", http.MethodPost, "/tenants", "新建工作区")
	create.DefaultStatus = http.StatusCreated

	rest.Register(api, route(requireRead, "tenant-list", http.MethodGet, "/tenants", "工作区列表"), NewList(db, logins, addr).HTTP)
	rest.Register(api, route(requireRead, "tenant-get", http.MethodGet, "/tenants/{id}", "读取工作区"), NewGet(db, logins, addr).HTTP)
	rest.Register(api, create, NewCreate(db, logins, addr).HTTP)
	rest.Register(api, route(requireManage, "tenant-update", http.MethodPut, "/tenants/{id}", "修改工作区"), NewUpdate(db, logins, addr).HTTP)
	rest.Register(api, route(requireManage, "tenant-disable", http.MethodPost, "/tenants/{id}/disable", "停用工作区"), NewDisable(db, logins, addr).HTTP)
	rest.Register(api, route(requireManage, "tenant-enable", http.MethodPost, "/tenants/{id}/enable", "启用工作区"), NewEnable(db, logins, addr).HTTP)
	rest.Register(api, route(requireManage, "tenant-change-owner", http.MethodPut, "/tenants/{id}/owner", "更换主管理员"), NewChangeOwner(db, logins, addr).HTTP)

	api.Resource(rest.Resource{
		Name:       "tenant",
		List:       "tenant-list",
		Get:        "tenant-get",
		Create:     "tenant-create",
		Update:     "tenant-update",
		Columns:    []string{"name", "slug", "owner_name", "owner_email", "status", "created_at"},
		Filters:    []string{"name", "status"},
		CreateForm: []string{"name", "slug", "owner_email", "owner_name", "owner_password"},
		Actions: []rest.Action{
			// 停用只在正常的工作区上显示，启用只在已停用的上显示
			{Name: "disable", Operation: "tenant-disable", Confirm: true, Danger: true, When: map[string][]string{"status": {string(Active)}}},
			{Name: "enable", Operation: "tenant-enable", Confirm: true, When: map[string][]string{"status": {string(Disabled)}}},
		},
	})
}
