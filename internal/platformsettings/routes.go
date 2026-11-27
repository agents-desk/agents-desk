package platformsettings

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/settings"
)

// TestMailDeps 携带测试邮件发送接口所需的登录域与邮件模板依赖。
type TestMailDeps struct {
	Logins    *login.Realm
	Templates *mail.Templates
}

// RegisterRoutes 注册平台常规设置、邮件服务设置与测试邮件接口，并通过 api.Settings 声明给前端 SettingsPage。
// requireManage 要求 platform-settings:manage 权限；files 用于确认和替换平台 Logo 文件。
func RegisterRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, general *settings.Group[General], mailGroup *settings.Group[Mail], files *filesystem.Service, testDeps ...TestMailDeps) {
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"settings"}}
		requireManage(&op)
		return op
	}
	var td TestMailDeps
	if len(testDeps) > 0 {
		td = testDeps[0]
	}
	rest.Register(api, route("general-settings-get", http.MethodGet, "/settings/general", "读取常规设置"), NewGetGeneral(general).HTTP)
	rest.Register(api, route("general-settings-update", http.MethodPut, "/settings/general", "保存常规设置"), NewSaveGeneral(db, general, files).HTTP)
	rest.Register(api, route("mail-settings-get", http.MethodGet, "/settings/mail", "读取邮件服务设置"), NewGetMail(mailGroup).HTTP)
	rest.Register(api, route("mail-settings-update", http.MethodPut, "/settings/mail", "保存邮件服务设置"), NewSaveMail(db, mailGroup).HTTP)
	rest.Register(api, route("mail-settings-test", http.MethodPost, "/settings/mail/test", "发送测试邮件"), NewTestMail(db, general, mailGroup, td.Logins, td.Templates).HTTP)

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

// RegisterPublicRoutes 注册免登录的平台公开信息接口（GET /site），供管理端与工作区端读取平台名称和 Logo。
func RegisterPublicRoutes(api *rest.API, general *settings.Group[General], files *filesystem.Service) {
	op := huma.Operation{
		OperationID: "site-get",
		Method:      http.MethodGet,
		Path:        "/site",
		Summary:     "读取平台公开信息",
		Tags:        []string{"site"},
	}
	rest.Public()(&op)
	rest.Register(api, op, NewGetPublicSite(general, files).HTTP)
}
