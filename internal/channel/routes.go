package channel

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册渠道的只读列表接口。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	op := huma.Operation{OperationID: "channel-list", Method: http.MethodGet, Path: "/channels", Summary: "渠道列表"}
	require(auth.ChannelsRead)(&op)
	rest.Register(api, op, NewList(db).HTTP)
}
