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
	manageSettings := require(auth.TenantSettingsManage)
	replyOp := route(reply, "conversation-reply", http.MethodPost, "/conversations/{id}/replies", "回复会话")
	replyOp.DefaultStatus = http.StatusCreated
	noteOp := route(reply, "conversation-note", http.MethodPost, "/conversations/{id}/notes", "写内部备注")
	noteOp.DefaultStatus = http.StatusCreated
	retryOp := route(reply, "conversation-retry", http.MethodPost, "/conversations/{id}/items/{item_id}/retry", "重发发送失败的消息")
	readOp := route(read, "conversation-read", http.MethodPost, "/conversations/{id}/read", "标记已读")
	readOp.DefaultStatus = http.StatusNoContent
	viewCreateOp := route(read, "view-create", http.MethodPost, "/views", "新建收件箱视图")
	viewCreateOp.DefaultStatus = http.StatusCreated
	viewDeleteOp := route(read, "view-delete", http.MethodDelete, "/views/{id}", "删除收件箱视图")
	viewDeleteOp.DefaultStatus = http.StatusNoContent
	viewReorderOp := route(read, "view-reorder", http.MethodPut, "/views/reorder", "调整收件箱视图顺序")
	viewReorderOp.DefaultStatus = http.StatusNoContent
	// 会话的操作：权限在业务动作中检查（指派要看是不是领取、是不是自己负责的会话），路由只要求能处理会话
	act := func(id, method, path, summary string) huma.Operation { return route(reply, id, method, path, summary) }

	assignSettings := NewAssignmentSettingsActions(db)
	rest.Register(api, route(read, "assignment-settings-get", http.MethodGet, "/settings/assignment", "读取工作区默认指派设置"), assignSettings.GetHTTP)
	rest.Register(api, route(manageSettings, "assignment-settings-update", http.MethodPut, "/settings/assignment", "修改工作区默认指派设置"), assignSettings.UpdateHTTP)
	rest.Register(api, route(read, "conversation-inbox", http.MethodGet, "/inbox", "收件箱的列表和数量"), NewGetInbox(db).HTTP)
	rest.Register(api, route(read, "view-list", http.MethodGet, "/views", "收件箱视图列表"), NewListViews(db).HTTP)
	rest.Register(api, route(read, "view-get", http.MethodGet, "/views/{id}", "读取收件箱视图"), NewGetView(db).HTTP)
	rest.Register(api, viewCreateOp, NewCreateView(db, down).HTTP)
	rest.Register(api, route(read, "view-update", http.MethodPut, "/views/{id}", "修改收件箱视图"), NewUpdateView(db, down).HTTP)
	rest.Register(api, viewDeleteOp, NewDeleteView(db, down).HTTP)
	rest.Register(api, viewReorderOp, NewReorderViews(db, down).HTTP)
	rest.Register(api, route(read, "conversation-list", http.MethodGet, "/conversations", "会话列表"), NewList(db).HTTP)
	rest.Register(api, route(read, "conversation-search", http.MethodGet, "/conversations/search", "全文搜索会话"), NewSearch(db).HTTP)
	rest.Register(api, route(read, "conversation-get", http.MethodGet, "/conversations/{id}", "读取会话"), NewGet(db).HTTP)
	rest.Register(api, route(read, "conversation-items", http.MethodGet, "/conversations/{id}/items", "读取时间线"), NewTimeline(db, down.Media).HTTP)
	rest.Register(api, route(read, "conversation-rounds", http.MethodGet, "/conversations/{id}/rounds", "读取服务轮次记录"), NewListRounds(db).HTTP)
	rest.Register(api, replyOp, NewReply(db, down).HTTP)
	rest.Register(api, noteOp, NewAddNote(db, down).HTTP)
	rest.Register(api, retryOp, NewRetry(db, down).HTTP)
	rest.Register(api, readOp, NewMarkRead(db, down.Hub).HTTP)
	rest.Register(api, act("conversation-assign", http.MethodPut, "/conversations/{id}/assignment", "指派"), NewAssign(db, down).HTTP)
	rest.Register(api, act("conversation-close", http.MethodPost, "/conversations/{id}/close", "关闭"), NewClose(db, down).HTTP)
	rest.Register(api, act("conversation-reopen", http.MethodPost, "/conversations/{id}/reopen", "重新打开"), NewReopen(db, down).HTTP)
	rest.Register(api, act("conversation-snooze", http.MethodPost, "/conversations/{id}/snooze", "稍后处理"), NewSnooze(db, down).HTTP)
	rest.Register(api, act("conversation-priority", http.MethodPut, "/conversations/{id}/priority", "修改优先级"), NewSetPriority(db, down).HTTP)
	rest.Register(api, act("conversation-language", http.MethodPut, "/conversations/{id}/language", "设置对方的语言"), NewSetLanguage(db, down).HTTP)
	rest.Register(api, act("conversation-tag-add", http.MethodPost, "/conversations/{id}/tags", "给会话添加标签"), NewAddTag(db, down).HTTP)
	rest.Register(api, act("conversation-tag-remove", http.MethodDelete, "/conversations/{id}/tags/{tag_id}", "移除会话标签"), NewRemoveTag(db, down).HTTP)
	rest.Register(api, act("conversation-attributes-patch", http.MethodPatch, "/conversations/{id}/attributes", "修改会话属性"), NewPatchAttributes(db, down).HTTP)
	rest.Register(api, act("conversation-contact-tag-add", http.MethodPost, "/conversations/{id}/contact/tags", "给联系人添加标签"), NewAddContactTag(db, down).HTTP)
	rest.Register(api, act("conversation-contact-tag-remove", http.MethodDelete, "/conversations/{id}/contact/tags/{tag_id}", "移除联系人标签"), NewRemoveContactTag(db, down).HTTP)
	rest.Register(api, act("conversation-contact-attributes-patch", http.MethodPatch, "/conversations/{id}/contact/attributes", "修改联系人属性"), NewPatchContactAttributes(db, down).HTTP)
	rest.Register(api, act("conversation-bulk-assign", http.MethodPost, "/conversations/bulk/assign", "批量指派"), NewBulkAssign(db, down).HTTP)
	rest.Register(api, act("conversation-bulk-close", http.MethodPost, "/conversations/bulk/close", "批量关闭"), NewBulkClose(db, down).HTTP)
	rest.Register(api, act("conversation-apply-actions", http.MethodPost, "/conversations/{id}/actions", "批量执行会话操作"), NewApplyActions(db, down).HTTP)
}
