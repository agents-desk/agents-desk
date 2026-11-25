package platformoverview

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/tenant"
)

// RegisterRoutes 在管理员端 API 注册平台概览接口。requireAdmin 仅要求平台管理员登录，接口内部按权限返回对应模块。
func RegisterRoutes(api *rest.API, requireAdmin func(*huma.Operation), db *bun.DB, files *filesystem.Service, addr tenant.Addresses) {
	op := huma.Operation{
		OperationID: "overview-get",
		Method:      http.MethodGet,
		Path:        "/overview",
		Summary:     "平台运行概览",
		Tags:        []string{"overview"},
	}
	requireAdmin(&op)
	rest.Register(api, op, NewGet(db, files, addr).HTTP)
}
