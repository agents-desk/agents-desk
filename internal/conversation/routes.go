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

	rest.Register(api, route(read, "conversation-list", http.MethodGet, "/conversations", "会话列表"), NewList(db).HTTP)
	rest.Register(api, route(read, "conversation-get", http.MethodGet, "/conversations/{id}", "读取会话"), NewGet(db).HTTP)
	rest.Register(api, route(read, "conversation-items", http.MethodGet, "/conversations/{id}/items", "读取时间线"), NewTimeline(db).HTTP)
	rest.Register(api, replyOp, NewReply(db, down).HTTP)
	rest.Register(api, noteOp, NewAddNote(db, down).HTTP)
}
