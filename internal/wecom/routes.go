package wecom

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区 API（/t/{slug}/api）中注册企业微信智能机器人管理接口，并将其声明为资源。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, c *crypt.Crypt, mgr *Manager) {
	manage := require(auth.ChannelsManage)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		manage(&op)
		return op
	}
	var wake func()
	var restart func(string)
	if mgr != nil {
		wake = mgr.Wake
		restart = mgr.Restart
	}
	create := route("wecom-create", http.MethodPost, "/wecom-bots", "新建企业微信机器人")
	create.DefaultStatus = http.StatusCreated

	rest.Register(api, route("wecom-list", http.MethodGet, "/wecom-bots", "企业微信机器人列表"), NewList(db).HTTP)
	rest.Register(api, route("wecom-get", http.MethodGet, "/wecom-bots/{id}", "读取企业微信机器人"), NewGet(db).HTTP)
	rest.Register(api, create, NewCreate(db, c, wake).HTTP)
	rest.Register(api, route("wecom-update", http.MethodPut, "/wecom-bots/{id}", "修改企业微信机器人"), NewUpdate(db, c, restart).HTTP)
	rest.Register(api, route("wecom-reconnect", http.MethodPost, "/wecom-bots/{id}/reconnect", "重连企业微信机器人"), NewReconnect(db, restart).HTTP)

	api.Resource(rest.Resource{
		Name:       "wecom",
		List:       "wecom-list",
		Get:        "wecom-get",
		Create:     "wecom-create",
		Update:     "wecom-update",
		Columns:    []string{"name", "bot_id", "team_name", "status", "enabled"},
		CreateForm: []string{"name", "team_id", "bot_id", "secret", "ws_url", "welcome_text"},
		UpdateForm: []string{"name", "team_id", "bot_id", "secret", "ws_url", "welcome_text", "enabled"},
		Refs:       map[string]string{"team_id": "team"},
	})
}
