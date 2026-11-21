package widget

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
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
		CreateForm: []string{"name", "team_id", "greeting", "color", "allowed_origins"},
		UpdateForm: []string{"name", "team_id", "greeting", "color", "allowed_origins", "enabled"},
	})
}

// ConversationInput 是指定客户的一个会话的请求。
type ConversationInput struct {
	ID string `path:"id" format:"uuid"`
}

// ConversationListOutput 是客户的会话列表。
type ConversationListOutput struct {
	Body []conversation.ContactConversationDTO
}

// ConversationOutput 是客户的一个会话。
type ConversationOutput struct {
	Body conversation.ContactConversationDTO
}

// NewConversationInput 是客户发起新会话的请求：第一条消息。
type NewConversationInput struct {
	Body MessageBody
}

// MessageInput 是客户在会话中发消息的请求。
type MessageInput struct {
	ID   string `path:"id" format:"uuid"`
	Body MessageBody
}

// ItemsInput 是客户读取时间线的请求：从序号 after 之后读，最多 limit 项。
type ItemsInput struct {
	ID    string `path:"id" format:"uuid"`
	After int64  `query:"after" minimum:"0" doc:"只读取序号大于它的项；收到推送后用它增量读取"`
	Limit int    `query:"limit" minimum:"1" maximum:"500" default:"100"`
}

// ItemsOutput 是客户看到的时间线中的一段，按序号从小到大。
type ItemsOutput struct {
	Body []conversation.ContactItemDTO
}

// RateInput 是客户评价已关闭的会话。
type RateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Score   int    `json:"score" minimum:"1" maximum:"5" doc:"1 到 5 星"`
		Comment string `json:"comment,omitempty" maxLength:"1000" doc:"附的一句意见"`
	}
}

// ReadInput 是客户读到了会话中序号 seq 及之前的消息。
type ReadInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Seq int64 `json:"seq" minimum:"0" doc:"读到的最后一项的序号"`
	}
}

// RegisterVisitorRoutes 在聊天插件的 API（/w/{channel}/api，前面是 Middleware）中注册客户用的接口：公开设置、建立会话，
// 以及客户以联系人的身份读取自己的会话和时间线、发消息、标记已读、评价已关闭的会话、告诉成员自己正在输入。requireContact 要求联系人的令牌（Authorization: Bearer）；
// tokens 签发联系人的令牌，registry 和 down 交给写入收到的消息的动作。
func RegisterVisitorRoutes(api *rest.API, requireContact func(*huma.Operation), db *bun.DB, c *crypt.Crypt, tokens *apitoken.Store, receive *conversation.Receive, down conversation.Downstream) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	public := rest.Public()
	start := route(requireContact, "conversation-start", http.MethodPost, "/conversations", "发起新会话")
	start.DefaultStatus = http.StatusCreated
	send := route(requireContact, "conversation-send", http.MethodPost, "/conversations/{id}/messages", "发消息")
	send.DefaultStatus = http.StatusCreated
	read := route(requireContact, "conversation-read", http.MethodPost, "/conversations/{id}/read", "标记已读")
	read.DefaultStatus = http.StatusNoContent
	rateOp := route(requireContact, "conversation-rate", http.MethodPost, "/conversations/{id}/rating", "评价会话")
	typingOp := route(requireContact, "conversation-typing", http.MethodPost, "/conversations/{id}/typing", "正在输入")
	typingOp.DefaultStatus = http.StatusNoContent

	sendMessage := NewSendMessage(db, receive)
	list, get, items := conversation.NewListForContact(db), conversation.NewGetForContact(db), conversation.NewTimelineForContact(db)
	markRead := conversation.NewMarkReadByContact(db, down)
	rate := conversation.NewRateByContact(db, down)
	typing := conversation.NewTypingByContact(db, down)

	rest.Register(api, route(public, "widget-config", http.MethodGet, "/config", "聊天插件的公开设置"), NewGetConfig().HTTP)
	rest.Register(api, route(public, "widget-session", http.MethodPost, "/session", "建立会话"), NewStartSession(db, c, tokens).HTTP)

	// 以下是联系人的接口：只做输入转换，渠道是本次请求的聊天插件，归属由业务动作检查
	rest.Register(api, route(requireContact, "conversation-list", http.MethodGet, "/conversations", "我的会话"),
		func(ctx context.Context, _ *struct{}) (*ConversationListOutput, error) {
			sub, ch, err := visitor(ctx)
			if err != nil {
				return nil, err
			}
			out, err := list.Handle(ctx, sub, ch)
			if err != nil {
				return nil, err
			}
			return &ConversationListOutput{Body: out}, nil
		})
	rest.Register(api, start, func(ctx context.Context, in *NewConversationInput) (*SentOutput, error) {
		sub, _ := identity.FromContext(ctx)
		out, err := sendMessage.Handle(ctx, sub, "", in.Body)
		if err != nil {
			return nil, err
		}
		return &SentOutput{Body: *out}, nil
	})
	rest.Register(api, route(requireContact, "conversation-get", http.MethodGet, "/conversations/{id}", "读取会话"),
		func(ctx context.Context, in *ConversationInput) (*ConversationOutput, error) {
			sub, ch, err := visitor(ctx)
			if err != nil {
				return nil, err
			}
			out, err := get.Handle(ctx, sub, ch, in.ID)
			if err != nil {
				return nil, err
			}
			return &ConversationOutput{Body: *out}, nil
		})
	rest.Register(api, route(requireContact, "conversation-items", http.MethodGet, "/conversations/{id}/items", "读取时间线"),
		func(ctx context.Context, in *ItemsInput) (*ItemsOutput, error) {
			sub, ch, err := visitor(ctx)
			if err != nil {
				return nil, err
			}
			out, err := items.Handle(ctx, sub, ch, in.ID, in.After, in.Limit)
			if err != nil {
				return nil, err
			}
			return &ItemsOutput{Body: out}, nil
		})
	rest.Register(api, send, func(ctx context.Context, in *MessageInput) (*SentOutput, error) {
		sub, _ := identity.FromContext(ctx)
		out, err := sendMessage.Handle(ctx, sub, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return &SentOutput{Body: *out}, nil
	})
	rest.Register(api, rateOp, func(ctx context.Context, in *RateInput) (*ConversationOutput, error) {
		sub, ch, err := visitor(ctx)
		if err != nil {
			return nil, err
		}
		out, err := rate.Handle(ctx, sub, ch, in.ID, in.Body.Score, in.Body.Comment)
		if err != nil {
			return nil, err
		}
		return &ConversationOutput{Body: *out}, nil
	})
	rest.Register(api, typingOp, func(ctx context.Context, in *ConversationInput) (*struct{}, error) {
		sub, ch, err := visitor(ctx)
		if err != nil {
			return nil, err
		}
		return nil, typing.Handle(ctx, sub, ch, in.ID)
	})
	rest.Register(api, read, func(ctx context.Context, in *ReadInput) (*struct{}, error) {
		sub, ch, err := visitor(ctx)
		if err != nil {
			return nil, err
		}
		return nil, markRead.Handle(ctx, sub, ch, in.ID, in.Body.Seq)
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
