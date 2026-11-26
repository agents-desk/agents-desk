package attribute

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册自定义属性定义。
// 工作区成员都能读取属性定义；写操作在业务动作中按对象检查权限：工单属性要求 ticket-types:manage，其余属性要求 tenant-settings:manage。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	member := require()
	create := route(member, "attribute-create", http.MethodPost, "/attributes", "新建属性")
	create.DefaultStatus = http.StatusCreated
	del := route(member, "attribute-delete", http.MethodDelete, "/attributes/{id}", "删除属性")
	del.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(member, "attribute-list", http.MethodGet, "/attributes", "属性列表"), NewList(db).HTTP)
	rest.Register(api, route(member, "attribute-reorder", http.MethodPut, "/attributes/order", "调整属性顺序"), NewReorder(db).HTTP)
	rest.Register(api, route(member, "attribute-get", http.MethodGet, "/attributes/{id}", "读取属性"), NewGet(db).HTTP)
	rest.Register(api, create, NewCreate(db).HTTP)
	rest.Register(api, route(member, "attribute-update", http.MethodPut, "/attributes/{id}", "修改属性"), NewUpdate(db).HTTP)
	rest.Register(api, del, NewDelete(db).HTTP)
}
