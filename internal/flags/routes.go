package flags

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/feature/featureapi"
	"github.com/go-tenon/tenon/httpx/rest"
)

// RegisterRoutes 在管理员端 API 注册功能开关管理接口，requireManage 要求 features:manage 权限。
func RegisterRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, store *feature.Store) {
	featureapi.Register(api, requireManage, db, store, Options(db))
}
