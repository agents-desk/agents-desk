package contact

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/sheet/exports"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册联系人的列表、概览、视图、导出、读取和修改接口。
// require 返回要求成员登录凭据和给定权限的访问策略。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, hub *broadcast.Hub, roles *authz.Store, exporter *exports.Service) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	read, update, exportPerm := require(auth.ContactsRead), require(auth.ContactsUpdate), require(auth.ContactsExport)

	views := NewViewActions(db, hub, roles)
	viewCreateOp := route(read, "contact-view-create", http.MethodPost, "/contacts/views", "新建联系人筛选视图")
	viewCreateOp.DefaultStatus = http.StatusCreated
	viewDeleteOp := route(read, "contact-view-delete", http.MethodDelete, "/contacts/views/{id}", "删除联系人筛选视图")
	viewDeleteOp.DefaultStatus = http.StatusNoContent
	viewReorderOp := route(read, "contact-view-reorder", http.MethodPut, "/contacts/views/reorder", "调整联系人筛选视图顺序")
	viewReorderOp.DefaultStatus = http.StatusNoContent

	exp := NewQueueExport(db, hub, exporter)
	exportStartOp := route(exportPerm, "contact-export-start", http.MethodPost, "/contacts/exports", "发起联系人后台导出")
	exportStartOp.DefaultStatus = http.StatusAccepted
	exportProgressOp := route(exportPerm, "contact-export-progress", http.MethodGet, "/contacts/exports/{id}/progress", "联系人导出进度")

	rest.Register(api, route(read, "contact-summary", http.MethodGet, "/contacts/summary", "联系人的数量、渠道和视图列表"), NewGetSummary(db).HTTP)
	rest.Register(api, route(read, "contact-view-list", http.MethodGet, "/contacts/views", "联系人筛选视图列表"), views.ListHTTP)
	rest.Register(api, viewCreateOp, views.CreateHTTP)
	rest.Register(api, route(read, "contact-view-update", http.MethodPut, "/contacts/views/{id}", "修改联系人筛选视图"), views.UpdateHTTP)
	rest.Register(api, viewDeleteOp, views.DeleteHTTP)
	rest.Register(api, viewReorderOp, views.ReorderHTTP)
	rest.Register(api, exportStartOp, exp.HTTP)
	rest.RegisterSSE(api, exportProgressOp, map[string]any{"progress": ExportProgress{}}, exp.Progress)
	rest.Register(api, route(read, "contact-list", http.MethodGet, "/contacts", "联系人列表"), NewList(db).HTTP)
	rest.Register(api, route(read, "contact-get", http.MethodGet, "/contacts/{id}", "读取联系人"), NewGet(db).HTTP)
	rest.Register(api, route(update, "contact-update", http.MethodPut, "/contacts/{id}", "修改联系人资料"), NewUpdate(db).HTTP)
}
