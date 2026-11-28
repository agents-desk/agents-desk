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

// GenerationOutput 是单条生成的响应。
type GenerationOutput struct {
	Body GenerationDTO
}

// GenerationListOutput 是会话中全部进行中生成的响应。
type GenerationListOutput struct {
	Body []GenerationDTO
}

// BotIDInput 是指定一个机器人的路径参数。
type BotIDInput struct {
	ID string `path:"id" format:"uuid"`
}

// BotListInput 是机器人列表的查询参数。
type BotListInput struct {
	Enabled bool `query:"enabled" doc:"仅筛选启用状态为 true 的机器人"`
}

// BotListOutput 是机器人列表响应。
type BotListOutput struct {
	Body []BotDTO
}

// BotOutput 是单个机器人资料响应。
type BotOutput struct {
	Body BotDTO
}

// CreateBotInput 是新建机器人请求。
type CreateBotInput struct {
	Body BotInput
}

// UpdateBotInput 是修改机器人请求。
type UpdateBotInput struct {
	ID   string `path:"id" format:"uuid"`
	Body BotInput
}

// ApprovalActionInput 是工具调用审批/拒绝请求。
type ApprovalActionInput struct {
	ID     string `path:"id" format:"uuid"`
	CallID string `path:"call_id" minLength:"1"`
	Body   ApprovalDecisionInput
}

// HTTP 处理 GET /t/{slug}/api/bots。
func (a *ListBots) HTTP(ctx context.Context, in *BotListInput) (*BotListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	bots, err := a.Handle(ctx, sub, in.Enabled)
	if err != nil {
		return nil, err
	}
	return &BotListOutput{Body: bots}, nil
}

// HTTP 处理 POST /t/{slug}/api/bots。
func (a *CreateBot) HTTP(ctx context.Context, in *CreateBotInput) (*BotOutput, error) {
	sub, _ := identity.FromContext(ctx)
	bot, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &BotOutput{Body: *bot}, nil
}

// HTTP 处理 PUT /t/{slug}/api/bots/{id}。
func (a *UpdateBot) HTTP(ctx context.Context, in *UpdateBotInput) (*BotOutput, error) {
	sub, _ := identity.FromContext(ctx)
	bot, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &BotOutput{Body: *bot}, nil
}

// HTTP 处理 DELETE /t/{slug}/api/bots/{id}。
func (a *DeleteBot) HTTP(ctx context.Context, in *BotIDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
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

// HTTPApprove 处理 POST /t/{slug}/api/generations/{id}/approvals/{call_id}/approve。
func (a *ApproveToolCall) HTTPApprove(ctx context.Context, in *ApprovalActionInput) (*GenerationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Approve(ctx, sub, in.ID, in.CallID)
	if err != nil {
		return nil, err
	}
	return &GenerationOutput{Body: *dto}, nil
}

// HTTPReject 处理 POST /t/{slug}/api/generations/{id}/approvals/{call_id}/reject。
func (a *ApproveToolCall) HTTPReject(ctx context.Context, in *ApprovalActionInput) (*GenerationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Reject(ctx, sub, in.ID, in.CallID, in.Body.Reason)
	if err != nil {
		return nil, err
	}
	return &GenerationOutput{Body: *dto}, nil
}

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册机器人管理、进行中生成、取消及工具审批路由。
// svc 允许为 nil（供离线生成 OpenAPI 文档使用）。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, hub *broadcast.Hub, svc *Service) {
	read := require(auth.ConversationsRead)
	reply := require(auth.ConversationsReply)
	manage := require(auth.TenantSettingsManage)

	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}

	var cancelHook func(context.Context, string, string)
	if svc != nil {
		cancelHook = svc.CancelRun
	}

	createBotOp := route(manage, "bot-create", http.MethodPost, "/bots", "新建机器人")
	createBotOp.DefaultStatus = http.StatusCreated
	delBotOp := route(manage, "bot-delete", http.MethodDelete, "/bots/{id}", "删除机器人")
	delBotOp.DefaultStatus = http.StatusNoContent
	cancelOp := route(read, "generation-cancel", http.MethodPost, "/generations/{id}/cancel", "取消生成")
	cancelOp.DefaultStatus = http.StatusNoContent

	rest.Register(api, route(read, "bot-list", http.MethodGet, "/bots", "机器人列表"), NewListBots(db).HTTP)
	rest.Register(api, createBotOp, NewCreateBot(db).HTTP)
	rest.Register(api, route(manage, "bot-update", http.MethodPut, "/bots/{id}", "更新机器人"), NewUpdateBot(db).HTTP)
	rest.Register(api, delBotOp, NewDeleteBot(db).HTTP)

	rest.Register(api, route(read, "generation-list", http.MethodGet, "/conversations/{id}/generations", "读取进行中的生成与草稿"), NewList(db).HTTP)
	rest.Register(api, cancelOp, NewCancel(db, hub, cancelHook).HTTP)

	approver := NewApproveToolCall(db, svc)
	rest.Register(api, route(reply, "generation-approval-approve", http.MethodPost, "/generations/{id}/approvals/{call_id}/approve", "批准工具调用"), approver.HTTPApprove)
	rest.Register(api, route(reply, "generation-approval-reject", http.MethodPost, "/generations/{id}/approvals/{call_id}/reject", "拒绝工具调用"), approver.HTTPReject)
}
