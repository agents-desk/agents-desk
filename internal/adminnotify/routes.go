package adminnotify

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/httpx/rest"
)

// RegisterRoutes 在管理员端 API 注册站内通知列表、标记已读与 SSE 未读角标流接口。
// requireAdmin 要求有效的平台管理员登录凭据；通知天然按登录主体隔离，无需额外权限。
func RegisterRoutes(api *rest.API, requireAdmin func(*huma.Operation), db *bun.DB, hub *broadcast.Hub) {
	h := NewHandlers(db, hub)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"notification"}}
		requireAdmin(&op)
		return op
	}
	readOp := route("notification-read", http.MethodPost, "/notifications/read", "标记通知已读")
	readOp.DefaultStatus = http.StatusNoContent
	readAllOp := route("notification-read-all", http.MethodPost, "/notifications/read-all", "全部标记已读")
	readAllOp.DefaultStatus = http.StatusNoContent

	rest.Register(api, route("notification-list", http.MethodGet, "/notifications", "通知列表"), h.List)
	rest.Register(api, readOp, h.Read)
	rest.Register(api, readAllOp, h.ReadAll)
	rest.RegisterSSE(api, route("notification-inbox", http.MethodGet, "/notifications/inbox", "实时未读通知角标"), map[string]any{"badge": Badge{}}, h.Inbox)
}
