package agent

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// ConversationInput 是指定一个会话的请求：路径中的会话 ID。
type ConversationInput struct {
	ID string `path:"id" format:"uuid"`
}

// GenerationInput 是指定一次生成的请求：路径中的生成 ID。
type GenerationInput struct {
	ID string `path:"id" format:"uuid"`
}

// GenerationListOutput 是会话中全部进行中生成的响应。
type GenerationListOutput struct {
	Body []GenerationDTO
}

// HTTP 处理 GET /t/{slug}/api/conversations/{id}/generations。
func (a *List) HTTP(ctx context.Context, in *ConversationInput) (*GenerationListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dtos, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &GenerationListOutput{Body: dtos}, nil
}

// HTTP 处理 POST /t/{slug}/api/generations/{id}/cancel。
func (a *Cancel) HTTP(ctx context.Context, in *GenerationInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册读取进行中的生成和取消生成。
// 两者都要求能读取会话；能不能操作具体会话由业务动作按可见范围判断。hub 用于把取消推送到生成频道。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, hub *broadcast.Hub) {
	read := require(auth.ConversationsRead)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		read(&op)
		return op
	}
	cancelOp := route("generation-cancel", http.MethodPost, "/generations/{id}/cancel", "取消生成")
	cancelOp.DefaultStatus = http.StatusNoContent
	rest.Register(api, route("generation-list", http.MethodGet, "/conversations/{id}/generations", "读取进行中的生成"), NewList(db).HTTP)
	rest.Register(api, cancelOp, NewCancel(db, hub).HTTP)
}
