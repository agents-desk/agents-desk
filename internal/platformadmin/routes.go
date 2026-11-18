package platformadmin

import (
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/login"
)

// RegisterRoutes 注册平台管理员的登录、退出和当前账号。requireAdmin 要求平台管理员的登录凭据；
// lifetime 是浏览器保存会话 cookie 的时长。
func RegisterRoutes(api *rest.API, requireAdmin func(*huma.Operation), db *bun.DB, logins *login.Realm, tokens *apitoken.Store, lifetime time.Duration) {
	s := NewSession(db, logins, tokens, lifetime)

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
}
