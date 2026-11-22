package platformsettings

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/settings"
)

// RegisterRoutes 注册平台常规设置与邮件服务设置接口，并通过 api.Settings 声明给前端 SettingsPage。
// requireManage 要求 platform-settings:manage 权限。
func RegisterRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, general *settings.Group[General], mailGroup *settings.Group[Mail]) {
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"settings"}}
		requireManage(&op)
		return op
	}
	rest.Register(api, route("general-settings-get", http.MethodGet, "/settings/general", "读取常规设置"), NewGetGeneral(general).HTTP)
	rest.Register(api, route("general-settings-update", http.MethodPut, "/settings/general", "保存常规设置"), NewSaveGeneral(db, general).HTTP)
	rest.Register(api, route("mail-settings-get", http.MethodGet, "/settings/mail", "读取邮件服务设置"), NewGetMail(mailGroup).HTTP)
	rest.Register(api, route("mail-settings-update", http.MethodPut, "/settings/mail", "保存邮件服务设置"), NewSaveMail(db, mailGroup).HTTP)

	api.Settings(rest.Settings{
		Name:   "general",
		Get:    "general-settings-get",
		Update: "general-settings-update",
	})
	api.Settings(rest.Settings{
		Name:   "mail",
		Get:    "mail-settings-get",
		Update: "mail-settings-update",
	})
}
