package telegram

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区 API（/t/{slug}/api）中注册 Telegram 机器人渠道管理接口，并将其声明为资源。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, c *crypt.Crypt, client *Client, publicURL string) {
	manage := require(auth.ChannelsManage)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		manage(&op)
		return op
	}
	create := route("telegram-create", http.MethodPost, "/telegrams", "新建 Telegram 机器人")
	create.DefaultStatus = http.StatusCreated

	rest.Register(api, route("telegram-list", http.MethodGet, "/telegrams", "Telegram 机器人列表"), NewList(db).HTTP)
	rest.Register(api, route("telegram-get", http.MethodGet, "/telegrams/{id}", "读取 Telegram 机器人"), NewGet(db).HTTP)
	rest.Register(api, create, NewCreate(db, c, client, publicURL).HTTP)
	rest.Register(api, route("telegram-update", http.MethodPut, "/telegrams/{id}", "修改 Telegram 机器人"), NewUpdate(db, c, client, publicURL).HTTP)
	rest.Register(api, route("telegram-installation", http.MethodGet, "/telegrams/{id}/installation", "Telegram 机器人的接入配置与 Webhook 密钥"),
		NewGetInstallation(db, c, publicURL).HTTP)
	rest.Register(api, route("telegram-rotate-secret", http.MethodPost, "/telegrams/{id}/webhook-secret", "更换 Webhook 密钥"),
		NewRotateSecret(db, c, client, publicURL).HTTP)

	api.Resource(rest.Resource{
		Name:       "telegram",
		List:       "telegram-list",
		Get:        "telegram-get",
		Create:     "telegram-create",
		Update:     "telegram-update",
		Columns:    []string{"name", "audience", "bot_username", "team_name", "enable_groups", "gateway_managed", "enabled"},
		CreateForm: []string{"name", "audience", "team_id", "bot_token", "enable_groups", "gateway_managed"},
		UpdateForm: []string{"name", "audience", "team_id", "bot_token", "enable_groups", "gateway_managed", "enabled"},
		Refs:       map[string]string{"team_id": "team"},
	})
}
