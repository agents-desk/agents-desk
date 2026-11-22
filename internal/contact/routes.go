package contact

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册联系人的列表、概览、读取和修改接口。
// require 返回要求成员登录凭据和给定权限的访问策略。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	read, update := require(auth.ContactsRead), require(auth.ContactsUpdate)

	rest.Register(api, route(read, "contact-summary", http.MethodGet, "/contacts/summary", "联系人的数量和渠道列表"), NewGetSummary(db).HTTP)
	rest.Register(api, route(read, "contact-list", http.MethodGet, "/contacts", "联系人列表"), NewList(db).HTTP)
	rest.Register(api, route(read, "contact-get", http.MethodGet, "/contacts/{id}", "读取联系人"), NewGet(db).HTTP)
	rest.Register(api, route(update, "contact-update", http.MethodPut, "/contacts/{id}", "修改联系人资料"), NewUpdate(db).HTTP)
}
