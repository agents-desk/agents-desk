package widget

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/file"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册聊天插件的管理：列表、新建、修改、嵌入代码和身份验证密钥，
// 并把聊天插件声明为资源。require 返回要求成员登录凭据和给定权限的访问策略；crypt 加密保存身份验证密钥，
// publicURL 是嵌入代码中加载聊天窗口的地址。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, c *crypt.Crypt, publicURL string) {
	manage := require(auth.ChannelsManage)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		manage(&op)
		return op
	}
	create := route("widget-create", http.MethodPost, "/widgets", "新建聊天插件")
	create.DefaultStatus = http.StatusCreated

	rest.Register(api, route("widget-list", http.MethodGet, "/widgets", "聊天插件列表"), NewList(db).HTTP)
	rest.Register(api, route("widget-get", http.MethodGet, "/widgets/{id}", "读取聊天插件"), NewGet(db).HTTP)
	rest.Register(api, create, NewCreate(db, c).HTTP)
	rest.Register(api, route("widget-update", http.MethodPut, "/widgets/{id}", "修改聊天插件"), NewUpdate(db).HTTP)
	rest.Register(api, route("widget-installation", http.MethodGet, "/widgets/{id}/installation", "聊天插件的嵌入代码和身份验证密钥"),
		NewGetInstallation(db, c, publicURL).HTTP)
	rest.Register(api, route("widget-rotate-secret", http.MethodPost, "/widgets/{id}/identity-secret", "更换身份验证密钥"),
		NewRotateSecret(db, c, publicURL).HTTP)

	api.Resource(rest.Resource{
		Name:       "widget",
		List:       "widget-list",
		Get:        "widget-get",
		Create:     "widget-create",
		Update:     "widget-update",
		Columns:    []string{"name", "team_name", "enabled"},
		CreateForm: []string{"name", "team_id"},
		UpdateForm: []string{"name", "team_id", "allowed_origins", "enabled"},
		Refs:       map[string]string{"team_id": "team"},
	})
}

// SendMessageInput 是客户发送消息的请求体包装。
type SendMessageInput struct {
	Body MessageBody
}

// TicketInput 是指定客户的一张工单的请求。
type TicketInput struct {
	ID string `path:"id" format:"uuid"`
}

// TicketListOutput 是客户可见的工单列表。
type TicketListOutput struct {
	Body []conversation.ContactTicketDTO
}

// TicketDetailOutput 是客户可见的单张工单详情。
type TicketDetailOutput struct {
	Body conversation.ContactTicketDetailDTO
}

// TimelineInput 是客户读取跨会话连续时间线的请求：支持向上翻历史（before）和收到推送后增量拉取（after）。
type TimelineInput struct {
	Before string `query:"before" format:"uuid" doc:"只读取早于该消息 ID 的更早历史消息，用于向上滚动分页"`
	After  string `query:"after" format:"uuid" doc:"只读取晚于该消息 ID 的新消息，用于收到实时推送后增量刷新"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}

// TimelineOutput 是客户跨会话连续时间线的一页响应。
type TimelineOutput struct {
	Body conversation.ContactTimelineDTO
}

// StreamReadInput 是客户在连续时间线中标记已读的请求。
type StreamReadInput struct {
	Body struct {
		UpToItemID string `json:"up_to_item_id,omitempty" format:"uuid" doc:"可选：截至已读的消息 ID；不填时将全部未读客服消息标为已读"`
	}
}

// RateInput 是客户评价已关闭会话轮次的请求。
type RateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Score   int    `json:"score" minimum:"1" maximum:"5" doc:"1 到 5 星"`
		Comment string `json:"comment,omitempty" maxLength:"1000" doc:"附的一句意见"`
	}
}

// RatedConversationOutput 是评价后的会话轮次摘要。
type RatedConversationOutput struct {
	Body conversation.ContactConversationMeta
}

// RegisterVisitorRoutes 在聊天插件的 API（/w/{channel}/api，前面是 Middleware）中注册客户用的接口：
// 公开设置、建立会话、上传附件、连续跨会话时间线、统一发消息、标记已读、正在输入、轮次评价、线索留邮箱与客户工单视图。
func RegisterVisitorRoutes(api *rest.API, requireContact func(*huma.Operation), db *bun.DB, c *crypt.Crypt, tokens *apitoken.Store, receive *conversation.Receive, down conversation.Downstream, maxUpload filesystem.ByteSize) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	public := rest.Public()
	streamSendOp := route(requireContact, "message-send", http.MethodPost, "/messages", "发送消息")
	streamSendOp.DefaultStatus = http.StatusCreated
	streamReadOp := route(requireContact, "timeline-read", http.MethodPost, "/read", "标记连续时间线已读")
	streamReadOp.DefaultStatus = http.StatusNoContent
	streamTypingOp := route(requireContact, "timeline-typing", http.MethodPost, "/typing", "在当前活跃会话中正在输入")
	streamTypingOp.DefaultStatus = http.StatusNoContent
	rateOp := route(requireContact, "conversation-rate", http.MethodPost, "/conversations/{id}/rating", "评价已关闭的会话")
	contactEmailOp := route(requireContact, "contact-email", http.MethodPost, "/contact/email", "线索留下邮箱")

	sendMessage := NewSendMessage(db, receive)
	leaveEmail := NewLeaveEmail(db, down)
	stream := conversation.NewContactStream(db, down.Media)
	markAllRead := conversation.NewMarkAllReadByContact(db, down)
	typingActive := conversation.NewTypingOnActiveByContact(db, down)
	rate := conversation.NewRateByContact(db, down)
	listTickets, getTicket := conversation.NewListTicketsForContact(db), conversation.NewGetTicketForContact(db, down.Media)

	file.RegisterRoutes(api, requireContact, down.Media.Files, maxUpload)

	rest.Register(api, route(public, "widget-config", http.MethodGet, "/config", "聊天插件的公开设置"), NewGetConfig().HTTP)
	rest.Register(api, route(public, "widget-session", http.MethodPost, "/session", "建立会话"), NewStartSession(db, c, tokens, down).HTTP)

	// 客户连续时间线与工单接口：渠道来自本次请求的聊天插件，归属由业务动作检查
	rest.Register(api, route(requireContact, "timeline-get", http.MethodGet, "/timeline", "读取客户跨会话连续时间线"),
		func(ctx context.Context, in *TimelineInput) (*TimelineOutput, error) {
			sub, ch, err := visitor(ctx)
			if err != nil {
				return nil, err
			}
			out, err := stream.Handle(ctx, sub, ch, in.Before, in.After, in.Limit)
			if err != nil {
				return nil, err
			}
			return &TimelineOutput{Body: *out}, nil
		})
	rest.Register(api, streamSendOp, func(ctx context.Context, in *SendMessageInput) (*SentOutput, error) {
		sub, _ := identity.FromContext(ctx)
		out, err := sendMessage.Handle(ctx, sub, in.Body)
		if err != nil {
			return nil, err
		}
		return &SentOutput{Body: *out}, nil
	})
	rest.Register(api, streamReadOp, func(ctx context.Context, in *StreamReadInput) (*struct{}, error) {
		sub, ch, err := visitor(ctx)
		if err != nil {
			return nil, err
		}
		return nil, markAllRead.Handle(ctx, sub, ch, in.Body.UpToItemID)
	})
	rest.Register(api, streamTypingOp, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		sub, ch, err := visitor(ctx)
		if err != nil {
			return nil, err
		}
		return nil, typingActive.Handle(ctx, sub, ch)
	})
	rest.Register(api, rateOp, func(ctx context.Context, in *RateInput) (*RatedConversationOutput, error) {
		sub, ch, err := visitor(ctx)
		if err != nil {
			return nil, err
		}
		out, err := rate.Handle(ctx, sub, ch, in.ID, in.Body.Score, in.Body.Comment)
		if err != nil {
			return nil, err
		}
		return &RatedConversationOutput{Body: *out}, nil
	})
	rest.Register(api, contactEmailOp, func(ctx context.Context, in *ContactEmailInput) (*ContactOutput, error) {
		sub, ch, err := visitor(ctx)
		if err != nil {
			return nil, err
		}
		out, err := leaveEmail.Handle(ctx, sub, ch, in.Body)
		if err != nil {
			return nil, err
		}
		return &ContactOutput{Body: *out}, nil
	})
	rest.Register(api, route(requireContact, "ticket-list", http.MethodGet, "/tickets", "我的工单"),
		func(ctx context.Context, _ *struct{}) (*TicketListOutput, error) {
			sub, ch, err := visitor(ctx)
			if err != nil {
				return nil, err
			}
			out, err := listTickets.Handle(ctx, sub, ch)
			if err != nil {
				return nil, err
			}
			return &TicketListOutput{Body: out}, nil
		})
	rest.Register(api, route(requireContact, "ticket-get", http.MethodGet, "/tickets/{id}", "读取工单详情"),
		func(ctx context.Context, in *TicketInput) (*TicketDetailOutput, error) {
			sub, ch, err := visitor(ctx)
			if err != nil {
				return nil, err
			}
			out, err := getTicket.Handle(ctx, sub, ch, in.ID)
			if err != nil {
				return nil, err
			}
			return &TicketDetailOutput{Body: *out}, nil
		})
}

// visitor 返回请求的调用者（联系人）和本次请求的聊天插件的 ID。
func visitor(ctx context.Context) (identity.Subject, string, error) {
	sub, _ := identity.FromContext(ctx)
	ch, err := channelFrom(ctx)
	if err != nil {
		return sub, "", err
	}
	return sub, ch.ID, nil
}
