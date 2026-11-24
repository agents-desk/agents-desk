package macro

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册快捷回复接口并声明为资源。
// 所有工作区成员都可以读取可见的快捷回复及管理自己的个人快捷回复；共享快捷回复的增删改在业务动作中检查 macros:manage。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, roles *authz.Store) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	member := require()
	create := route(member, "macro-create", http.MethodPost, "/macros", "新建快捷回复")
	create.DefaultStatus = http.StatusCreated
	del := route(member, "macro-delete", http.MethodDelete, "/macros/{id}", "删除快捷回复")
	del.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(member, "macro-list", http.MethodGet, "/macros", "快捷回复列表"), NewList(db).HTTP)
	rest.Register(api, route(member, "macro-get", http.MethodGet, "/macros/{id}", "读取快捷回复"), NewGet(db).HTTP)
	rest.Register(api, create, NewCreate(db, roles).HTTP)
	rest.Register(api, route(member, "macro-update", http.MethodPut, "/macros/{id}", "修改快捷回复"), NewUpdate(db, roles).HTTP)
	rest.Register(api, del, NewDelete(db).HTTP)

	api.Resource(rest.Resource{
		Name:       "macro",
		List:       "macro-list",
		Get:        "macro-get",
		Create:     "macro-create",
		Update:     "macro-update",
		Delete:     "macro-delete",
		Columns:    []string{"title", "scope", "body", "actions", "updated_at"},
		Filters:    []string{"q", "scope"},
		UpdateWhen: map[string][]string{"editable": {"true"}},
		DeleteWhen: map[string][]string{"editable": {"true"}},
	})
}
