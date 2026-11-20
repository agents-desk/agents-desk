package llm

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/settings"
)

// RegisterRoutes 注册读取全部调用类型、保存一种调用类型的模型接口的接口，requireManage 要求 platform-settings:manage。
func RegisterRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, group *settings.Group[Settings]) {
	get := huma.Operation{OperationID: "llm-settings-get", Method: http.MethodGet, Path: "/settings/model", Summary: "读取模型接口设置"}
	requireManage(&get)
	rest.Register(api, get, NewGetSettings(group).HTTP)

	save := huma.Operation{OperationID: "llm-endpoint-save", Method: http.MethodPut, Path: "/settings/model/{call_type}", Summary: "保存一种调用类型的模型接口"}
	requireManage(&save)
	rest.Register(api, save, NewSaveEndpoint(db, group).HTTP)
}
