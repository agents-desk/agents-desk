package flags

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/httpx/rest"
)

// RegisterRoutes 在管理员端 API 中注册功能开关的列表、设置与清除接口。
// requireManage 要求 features:manage 权限。
func RegisterRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, store *feature.Store) {
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"feature"}}
		requireManage(&op)
		return op
	}
	rest.Register(api, route("feature-list", http.MethodGet, "/features", "功能开关列表"), NewList(db, store).HTTP)
	rest.Register(api, route("feature-set", http.MethodPut, "/features/{name}", "设置功能开关"), NewSet(db, store).HTTP)
	rest.Register(api, route("feature-clear", http.MethodDelete, "/features/{name}", "清除功能开关设置"), NewClear(db, store).HTTP)
}
