package llm

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/settings"
)

// RegisterRoutes 注册读取与保存模型网关、调用类型及工作区预算覆写设置的接口，requireManage 要求 platform-settings:manage。
func RegisterRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, group *settings.Group[Settings]) {
	get := huma.Operation{OperationID: "llm-settings-get", Method: http.MethodGet, Path: "/settings/model", Summary: "读取模型网关与调用类型设置"}
	requireManage(&get)
	rest.Register(api, get, NewGetSettings(group).HTTP)

	save := huma.Operation{OperationID: "llm-settings-save", Method: http.MethodPut, Path: "/settings/model", Summary: "保存模型网关与调用类型设置"}
	requireManage(&save)
	rest.Register(api, save, NewSaveSettings(db, group).HTTP)

	tenantBudget := NewTenantBudgetGroup(db)
	getBudget := huma.Operation{OperationID: "llm-tenant-budget-get", Method: http.MethodGet, Path: "/tenants/{id}/llm-budget", Summary: "读取工作区模型预算上限"}
	requireManage(&getBudget)
	rest.Register(api, getBudget, NewGetTenantBudget(db, group, tenantBudget).HTTP)

	saveBudget := huma.Operation{OperationID: "llm-tenant-budget-save", Method: http.MethodPut, Path: "/tenants/{id}/llm-budget", Summary: "保存工作区模型预算覆写"}
	requireManage(&saveBudget)
	rest.Register(api, saveBudget, NewSaveTenantBudget(db, group, tenantBudget).HTTP)
}
