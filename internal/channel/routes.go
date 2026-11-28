package channel

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册渠道的列表与删除归档接口。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, onChanged ...func(Kind)) {
	op := huma.Operation{OperationID: "channel-list", Method: http.MethodGet, Path: "/channels", Summary: "渠道列表"}
	require(auth.ChannelsRead)(&op)
	rest.Register(api, op, NewList(db).HTTP)

	del := huma.Operation{OperationID: "channel-delete", Method: http.MethodDelete, Path: "/channels/{id}", Summary: "删除或归档公共渠道"}
	require(auth.ChannelsManage)(&del)
	rest.Register(api, del, NewDelete(db, onChanged...).HTTP)
}
