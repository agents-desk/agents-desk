package automation

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册自动化规则的列表与开关修改接口。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	manage := require(auth.TenantSettingsManage)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		manage(&op)
		return op
	}
	rest.Register(api, route("automation-list", http.MethodGet, "/automations", "自动化规则列表"), NewList(db).HTTP)
	rest.Register(api, route("automation-update", http.MethodPut, "/automations/{id}", "启用或停用自动化规则"), NewUpdate(db).HTTP)
}
