package platformadmin

import (
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
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
