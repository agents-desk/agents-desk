package tag

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册标签，并把标签声明为资源。
// 工作区成员都能读取标签用于筛选和展示；新建、修改、删除要求 tenant-settings:manage。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	read, manage := require(), require(auth.TenantSettingsManage)
	create := route(manage, "tag-create", http.MethodPost, "/tags", "新建标签")
	create.DefaultStatus = http.StatusCreated
	del := route(manage, "tag-delete", http.MethodDelete, "/tags/{id}", "删除标签")
	del.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(read, "tag-list", http.MethodGet, "/tags", "标签列表"), NewList(db).HTTP)
	rest.Register(api, route(read, "tag-get", http.MethodGet, "/tags/{id}", "读取标签"), NewGet(db).HTTP)
	rest.Register(api, create, NewCreate(db).HTTP)
	rest.Register(api, route(manage, "tag-update", http.MethodPut, "/tags/{id}", "修改标签"), NewUpdate(db).HTTP)
	rest.Register(api, del, NewDelete(db).HTTP)

	api.Resource(rest.Resource{
		Name:    "tag",
		List:    "tag-list",
		Get:     "tag-get",
		Create:  "tag-create",
		Update:  "tag-update",
		Delete:  "tag-delete",
		Columns: []string{"name", "conversation_count", "contact_count", "description"},
		Filters: []string{"name"},
	})
}
