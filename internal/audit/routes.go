package audit

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在管理员端 API 中注册平台操作日志接口，requireRead 要求 audit:read 权限。
func RegisterRoutes(api *rest.API, requireRead func(*huma.Operation), db *bun.DB) {
	op := huma.Operation{
		OperationID: "audit-list",
		Method:      http.MethodGet,
		Path:        "/audit",
		Summary:     "平台操作日志",
		Tags:        []string{"audit"},
	}
	requireRead(&op)
	rest.Register(api, op, NewList(db).HTTP)
}

// RegisterTenantRoutes 在工作区 API（/t/{slug}/api）中注册工作区操作日志接口，要求 tenant-settings:manage 权限。
func RegisterTenantRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	op := huma.Operation{
		OperationID: "tenant-audit-list",
		Method:      http.MethodGet,
		Path:        "/audit-logs",
		Summary:     "工作区操作日志",
		Tags:        []string{"audit"},
	}
	require(auth.TenantSettingsManage)(&op)
	rest.Register(api, op, NewTenantList(db).HTTP)
}
