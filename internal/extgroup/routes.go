package extgroup

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区接口（/t/{slug}/api）上登记外部群列表与配置接口。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	read := require(auth.ChannelsRead)
	manage := require(auth.ChannelsManage)

	listOp := huma.Operation{OperationID: "external-group-list", Method: http.MethodGet, Path: "/external-groups", Summary: "外部群列表"}
	read(&listOp)
	rest.Register(api, listOp, NewList(db).HTTP)

	updateOp := huma.Operation{OperationID: "external-group-update", Method: http.MethodPut, Path: "/external-groups/{id}", Summary: "更新外部群路由配置"}
	manage(&updateOp)
	rest.Register(api, updateOp, NewUpdate(db).HTTP)
}
