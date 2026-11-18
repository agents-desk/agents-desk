package llm

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/settings"
)

// RegisterRoutes 注册读取和保存模型接口设置的接口，requireManage 要求 platform-settings:manage。
func RegisterRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, group *settings.Group[Settings]) {
	get := huma.Operation{OperationID: "llm-settings-get", Method: http.MethodGet, Path: "/settings/model", Summary: "读取模型接口设置"}
	requireManage(&get)
	rest.Register(api, get, NewGetSettings(group).HTTP)

	save := huma.Operation{OperationID: "llm-settings-save", Method: http.MethodPut, Path: "/settings/model", Summary: "保存模型接口设置"}
	requireManage(&save)
	rest.Register(api, save, NewSaveSettings(db, group).HTTP)
}
