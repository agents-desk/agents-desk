package organization

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区接口（/t/{slug}/api）注册客户公司/组织的查询与管理接口。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	read := require(auth.ContactsRead)
	manage := require(auth.ContactsUpdate)

	listOp := huma.Operation{OperationID: "organization-list", Method: http.MethodGet, Path: "/organizations", Summary: "客户组织列表"}
	read(&listOp)
	rest.Register(api, listOp, NewList(db).HTTP)

	getOp := huma.Operation{OperationID: "organization-get", Method: http.MethodGet, Path: "/organizations/{id}", Summary: "读取客户组织"}
	read(&getOp)
	rest.Register(api, getOp, NewGet(db).HTTP)

	createOp := huma.Operation{OperationID: "organization-create", Method: http.MethodPost, Path: "/organizations", Summary: "新建客户组织"}
	manage(&createOp)
	rest.Register(api, createOp, NewCreate(db).HTTP)

	updateOp := huma.Operation{OperationID: "organization-update", Method: http.MethodPut, Path: "/organizations/{id}", Summary: "更新客户组织"}
	manage(&updateOp)
	rest.Register(api, updateOp, NewUpdate(db).HTTP)

	deleteOp := huma.Operation{OperationID: "organization-delete", Method: http.MethodDelete, Path: "/organizations/{id}", Summary: "删除客户组织"}
	deleteOp.DefaultStatus = http.StatusNoContent
	manage(&deleteOp)
	rest.Register(api, deleteOp, NewDelete(db).HTTP)
}
