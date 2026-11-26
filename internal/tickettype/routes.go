package tickettype

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册工单类型管理接口。
// 工作区成员都能读取工单类型；新建、修改、排序、停用、启用和删除要求 ticket-types:manage。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	read, manage := require(), require(auth.TicketTypesManage)
	create := route(manage, "ticket-type-create", http.MethodPost, "/ticket-types", "新建工单类型")
	create.DefaultStatus = http.StatusCreated
	del := route(manage, "ticket-type-delete", http.MethodDelete, "/ticket-types/{id}", "删除工单类型")
	del.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(read, "ticket-type-list", http.MethodGet, "/ticket-types", "工单类型列表"), NewList(db).HTTP)
	rest.Register(api, route(manage, "ticket-type-reorder", http.MethodPut, "/ticket-types/order", "调整工单类型顺序"), NewReorder(db).HTTP)
	rest.Register(api, route(read, "ticket-type-get", http.MethodGet, "/ticket-types/{id}", "读取工单类型"), NewGet(db).HTTP)
	rest.Register(api, create, NewCreate(db).HTTP)
	rest.Register(api, route(manage, "ticket-type-update", http.MethodPut, "/ticket-types/{id}", "修改工单类型"), NewUpdate(db).HTTP)
	rest.Register(api, route(manage, "ticket-type-disable", http.MethodPost, "/ticket-types/{id}/disable", "停用工单类型"), NewDisable(db).HTTP)
	rest.Register(api, route(manage, "ticket-type-enable", http.MethodPost, "/ticket-types/{id}/enable", "启用工单类型"), NewEnable(db).HTTP)
	rest.Register(api, del, NewDelete(db).HTTP)
}
