package platformadmin

import (
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/account"
	"agents-desk/internal/file"
)

// RegisterRoutes 注册平台管理员的登录、退出、当前账号、个人资料、修改密码和上传文件。files 保存上传的文件（头像），maxUpload 是单个文件的上限。requireAdmin 要求平台管理员的登录凭据；
// lifetime 是浏览器保存会话 cookie 的时长。
func RegisterRoutes(api *rest.API, requireAdmin func(*huma.Operation), db *bun.DB, logins *login.Realm, tokens *apitoken.Store, files *filesystem.Service, maxUpload filesystem.ByteSize, lifetime time.Duration) {
	s := NewSession(db, logins, tokens, files, lifetime)

	op := huma.Operation{OperationID: "auth-login", Method: http.MethodPost, Path: "/auth/login", Summary: "用邮箱和密码登录", DefaultStatus: http.StatusNoContent}
	rest.Public()(&op)
	rest.SameOrigin(api)(&op) // 只接受本站页面的登录，防止登录 CSRF
	rest.Register(api, op, s.Login)

	op = huma.Operation{OperationID: "auth-logout", Method: http.MethodPost, Path: "/auth/logout", Summary: "退出登录", DefaultStatus: http.StatusNoContent}
	requireAdmin(&op)
	rest.Register(api, op, s.Logout)

	pr := NewPasswordReset(logins, tokens)
	op = huma.Operation{OperationID: "admin-password-forgot", Method: http.MethodPost, Path: "/auth/password/forgot", Summary: "发送重置密码验证码邮件"}
	rest.Public()(&op)
	rest.SameOrigin(api)(&op)
	rest.Register(api, op, pr.ForgotHTTP)

	op = huma.Operation{OperationID: "admin-password-reset", Method: http.MethodPost, Path: "/auth/password/reset", Summary: "凭邮箱验证码重设密码", DefaultStatus: http.StatusNoContent}
	rest.Public()(&op)
	rest.SameOrigin(api)(&op)
	rest.Register(api, op, pr.ResetHTTP)

	op = huma.Operation{OperationID: "auth-me", Method: http.MethodGet, Path: "/me", Summary: "当前登录的平台管理员"}
	requireAdmin(&op)
	rest.Register(api, op, s.Me)

	// 个人资料：名字和头像（头像先用 file-upload 上传），以及修改密码。
	op = huma.Operation{OperationID: "auth-profile-update", Method: http.MethodPut, Path: "/me", Summary: "修改个人资料"}
	requireAdmin(&op)
	rest.Register(api, op, NewUpdateProfile(db, logins, files).HTTP)

	op = huma.Operation{OperationID: "auth-password-change", Method: http.MethodPut, Path: "/me/password", Summary: "修改密码", DefaultStatus: http.StatusNoContent}
	requireAdmin(&op)
	rest.Register(api, op, account.NewChangePassword(db, logins, tokens).HTTP)

	file.RegisterRoutes(api, requireAdmin, files, maxUpload)
}

// RegisterManageRoutes 注册平台管理员账号的增查改与停用/启用接口，并声明为管理员端的资源。
// requireManage 要求 platform-admins:manage 权限；creds 非 nil 时在停用管理员后通知长连接立即复查。
func RegisterManageRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, roles *authz.Store, logins *login.Realm, creds ...*broadcast.Credentials) {
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		requireManage(&op)
		return op
	}
	create := route("admin-create", http.MethodPost, "/admins", "新建平台管理员")
	create.DefaultStatus = http.StatusCreated

	rest.Register(api, route("admin-list", http.MethodGet, "/admins", "平台管理员列表"), NewList(db, logins).HTTP)
	rest.Register(api, route("admin-get", http.MethodGet, "/admins/{id}", "读取平台管理员"), NewGet(db, logins).HTTP)
	rest.Register(api, create, NewCreate(db, roles, logins).HTTP)
	rest.Register(api, route("admin-update", http.MethodPut, "/admins/{id}", "修改平台管理员"), NewUpdate(db, logins).HTTP)
	rest.Register(api, route("admin-disable", http.MethodPost, "/admins/{id}/disable", "停用平台管理员"), NewDisable(db, logins, creds...).HTTP)
	rest.Register(api, route("admin-enable", http.MethodPost, "/admins/{id}/enable", "启用平台管理员"), NewEnable(db, logins).HTTP)

	api.Resource(rest.Resource{
		Name:       "admin",
		List:       "admin-list",
		Get:        "admin-get",
		Create:     "admin-create",
		Update:     "admin-update",
		Columns:    []string{"name", "email", "status", "created_at"},
		Filters:    []string{"name", "status"},
		CreateForm: []string{"name", "email", "password"},
		Actions: []rest.Action{
			{Name: "disable", Operation: "admin-disable", Confirm: true, Danger: true, When: map[string][]string{"status": {string(Active)}, "self": {"false"}}},
			{Name: "enable", Operation: "admin-enable", Confirm: true, When: map[string][]string{"status": {string(Disabled)}}},
		},
	})
}
