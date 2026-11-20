package conversation

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册会话的读取、回复和内部备注。
// require 返回要求成员登录凭据和给定权限的访问策略；down 是时间线有新内容时通知的下游。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, down Downstream) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	read, reply := require(auth.ConversationsRead), require(auth.ConversationsReply)
	replyOp := route(reply, "conversation-reply", http.MethodPost, "/conversations/{id}/replies", "回复会话")
	replyOp.DefaultStatus = http.StatusCreated
	noteOp := route(reply, "conversation-note", http.MethodPost, "/conversations/{id}/notes", "写内部备注")
	noteOp.DefaultStatus = http.StatusCreated
	readOp := route(read, "conversation-read", http.MethodPost, "/conversations/{id}/read", "标记已读")
	readOp.DefaultStatus = http.StatusNoContent
	// 会话的操作：权限在业务动作中检查（指派要看是不是领取、是不是自己负责的会话），路由只要求能处理会话
	act := func(id, method, path, summary string) huma.Operation { return route(reply, id, method, path, summary) }

	rest.Register(api, route(read, "conversation-inbox", http.MethodGet, "/inbox", "收件箱的列表和数量"), NewGetInbox(db).HTTP)
	rest.Register(api, route(read, "conversation-list", http.MethodGet, "/conversations", "会话列表"), NewList(db).HTTP)
	rest.Register(api, route(read, "conversation-get", http.MethodGet, "/conversations/{id}", "读取会话"), NewGet(db).HTTP)
	rest.Register(api, route(read, "conversation-items", http.MethodGet, "/conversations/{id}/items", "读取时间线"), NewTimeline(db).HTTP)
	rest.Register(api, replyOp, NewReply(db, down).HTTP)
	rest.Register(api, noteOp, NewAddNote(db, down).HTTP)
	rest.Register(api, readOp, NewMarkRead(db).HTTP)
	rest.Register(api, act("conversation-assign", http.MethodPut, "/conversations/{id}/assignment", "指派"), NewAssign(db, down).HTTP)
	rest.Register(api, act("conversation-close", http.MethodPost, "/conversations/{id}/close", "关闭"), NewClose(db, down).HTTP)
	rest.Register(api, act("conversation-reopen", http.MethodPost, "/conversations/{id}/reopen", "重新打开"), NewReopen(db, down).HTTP)
	rest.Register(api, act("conversation-snooze", http.MethodPost, "/conversations/{id}/snooze", "稍后处理"), NewSnooze(db, down).HTTP)
	rest.Register(api, act("conversation-priority", http.MethodPut, "/conversations/{id}/priority", "修改优先级"), NewSetPriority(db, down).HTTP)
	rest.Register(api, act("conversation-bulk-assign", http.MethodPost, "/conversations/bulk/assign", "批量指派"), NewBulkAssign(db, down).HTTP)
	rest.Register(api, act("conversation-bulk-close", http.MethodPost, "/conversations/bulk/close", "批量关闭"), NewBulkClose(db, down).HTTP)
}
