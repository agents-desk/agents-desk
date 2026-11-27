package tgaccount

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// ListAccountsOutput 是当前成员 Telegram 个人号列表及平台配置状态响应。
type ListAccountsOutput struct {
	Body struct {
		Configured bool         `json:"configured" doc:"平台管理员是否已配置 Telegram API ID 与 API Hash"`
		Accounts   []AccountDTO `json:"accounts" nullable:"false"`
	}
}

// StartLoginInput 是启动交互式登录的请求。
type StartLoginInput struct {
	Body struct {
		Mode  LoginMode `json:"mode,omitempty" doc:"登录方式：qr（默认扫码）或 phone（手机号验证码）"`
		Name  string    `json:"name,omitempty" maxLength:"60" doc:"可选：自定义该账号在工作区中的显示名称"`
		Phone string    `json:"phone,omitempty" maxLength:"32" doc:"手机号登录时必填，带国际区号，例如 +8613800138000"`
	}
}

// LoginIDInput 是指定登录会话的路径参数。
type LoginIDInput struct {
	LoginID string `path:"login_id" format:"uuid"`
}

// SubmitCodeInput 是提交验证码的请求。
type SubmitCodeInput struct {
	LoginID string `path:"login_id" format:"uuid"`
	Body    struct {
		Code string `json:"code" minLength:"1" maxLength:"20" doc:"短信或 Telegram App 收到的登录验证码"`
	}
}

// SubmitPasswordInput 是提交 2FA 两步验证云密码的请求。
type SubmitPasswordInput struct {
	LoginID string `path:"login_id" format:"uuid"`
	Body    struct {
		Password string `json:"password" minLength:"1" maxLength:"200" doc:"Telegram 两步验证云密码"`
	}
}

// LoginSessionOutput 是登录会话状态响应。
type LoginSessionOutput struct {
	Body LoginSessionDTO
}

// AccountIDInput 是指定个人 Telegram 账号的路径参数。
type AccountIDInput struct {
	ID string `path:"id" format:"uuid"`
}

// UpdateAccountInput 是修改个人 Telegram 账号名称与启停状态的请求。
type UpdateAccountInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Name    string `json:"name" minLength:"1" maxLength:"60"`
		Enabled bool   `json:"enabled"`
	}
}

// AccountOutput 是单个个人 Telegram 账号响应。
type AccountOutput struct {
	Body AccountDTO
}

// DialogListOutput 是 Telegram 候选单聊与群聊列表响应。
type DialogListOutput struct {
	Body []DialogDTO
}

// UpdateSyncedPeersInput 是保存同步白名单并可选回填历史消息的请求。
type UpdateSyncedPeersInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Peers         []SyncedPeer `json:"peers" maxItems:"200" doc:"需要同步到商务沟通的 1:1 单聊与群聊列表"`
		BackfillLimit int          `json:"backfill_limit,omitempty" minimum:"0" maximum:"50" doc:"可选：为新增同步的会话拉取最近 N 条历史消息（默认 0 不拉取）"`
	}
}

// RegisterRoutes 在工作区 API（/t/{slug}/api）中注册成员自助绑定和管理 Telegram 个人号的全部接口。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), mgr *Manager) {
	use := require(auth.BusinessChannelsUse)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		use(&op)
		return op
	}
	disconnectOp := route("tgaccount-disconnect", http.MethodDelete, "/my/telegram-accounts/{id}", "解绑并断开 Telegram 个人号")
	disconnectOp.DefaultStatus = http.StatusNoContent

	rest.Register(api, route("tgaccount-list", http.MethodGet, "/my/telegram-accounts", "我的 Telegram 个人号列表"),
		func(ctx context.Context, _ *struct{}) (*ListAccountsOutput, error) {
			sub, _ := identity.FromContext(ctx)
			configured, err := mgr.Configured(ctx)
			if err != nil {
				return nil, err
			}
			accounts, err := mgr.ListAccounts(ctx, sub)
			if err != nil {
				return nil, err
			}
			out := &ListAccountsOutput{}
			out.Body.Configured = configured
			out.Body.Accounts = accounts
			return out, nil
		})

	rest.Register(api, route("tgaccount-login-start", http.MethodPost, "/my/telegram-accounts/login/start", "发起 Telegram 个人号登录"),
		func(ctx context.Context, in *StartLoginInput) (*LoginSessionOutput, error) {
			sub, _ := identity.FromContext(ctx)
			dto, err := mgr.StartLogin(ctx, sub, in.Body.Mode, in.Body.Name, in.Body.Phone)
			if err != nil {
				return nil, err
			}
			return &LoginSessionOutput{Body: *dto}, nil
		})

	rest.Register(api, route("tgaccount-login-get", http.MethodGet, "/my/telegram-accounts/login/{login_id}", "查询 Telegram 个人号登录状态"),
		func(ctx context.Context, in *LoginIDInput) (*LoginSessionOutput, error) {
			sub, _ := identity.FromContext(ctx)
			dto, err := mgr.GetLogin(ctx, sub, in.LoginID)
			if err != nil {
				return nil, err
			}
			return &LoginSessionOutput{Body: *dto}, nil
		})

	rest.Register(api, route("tgaccount-login-code", http.MethodPost, "/my/telegram-accounts/login/{login_id}/code", "提交 Telegram 登录验证码"),
		func(ctx context.Context, in *SubmitCodeInput) (*LoginSessionOutput, error) {
			sub, _ := identity.FromContext(ctx)
			dto, err := mgr.SubmitCode(ctx, sub, in.LoginID, in.Body.Code)
			if err != nil {
				return nil, err
			}
			return &LoginSessionOutput{Body: *dto}, nil
		})

	rest.Register(api, route("tgaccount-login-password", http.MethodPost, "/my/telegram-accounts/login/{login_id}/password", "提交 Telegram 两步验证密码"),
		func(ctx context.Context, in *SubmitPasswordInput) (*LoginSessionOutput, error) {
			sub, _ := identity.FromContext(ctx)
			dto, err := mgr.SubmitPassword(ctx, sub, in.LoginID, in.Body.Password)
			if err != nil {
				return nil, err
			}
			return &LoginSessionOutput{Body: *dto}, nil
		})

	rest.Register(api, route("tgaccount-update", http.MethodPut, "/my/telegram-accounts/{id}", "修改 Telegram 个人号设置"),
		func(ctx context.Context, in *UpdateAccountInput) (*AccountOutput, error) {
			sub, _ := identity.FromContext(ctx)
			dto, err := mgr.UpdateAccount(ctx, sub, in.ID, in.Body.Name, in.Body.Enabled)
			if err != nil {
				return nil, err
			}
			return &AccountOutput{Body: *dto}, nil
		})

	rest.Register(api, route("tgaccount-dialogs", http.MethodGet, "/my/telegram-accounts/{id}/dialogs", "读取 Telegram 可同步的单聊与群聊"),
		func(ctx context.Context, in *AccountIDInput) (*DialogListOutput, error) {
			sub, _ := identity.FromContext(ctx)
			items, err := mgr.ListDialogs(ctx, sub, in.ID)
			if err != nil {
				return nil, err
			}
			return &DialogListOutput{Body: items}, nil
		})

	rest.Register(api, route("tgaccount-synced-peers", http.MethodPut, "/my/telegram-accounts/{id}/synced-peers", "更新同步的 Telegram 单聊与群聊白名单"),
		func(ctx context.Context, in *UpdateSyncedPeersInput) (*AccountOutput, error) {
			sub, _ := identity.FromContext(ctx)
			dto, err := mgr.UpdateSyncedPeers(ctx, sub, in.ID, in.Body.Peers, in.Body.BackfillLimit)
			if err != nil {
				return nil, err
			}
			return &AccountOutput{Body: *dto}, nil
		})

	rest.Register(api, disconnectOp,
		func(ctx context.Context, in *AccountIDInput) (*struct{}, error) {
			sub, _ := identity.FromContext(ctx)
			return nil, mgr.DisconnectAccount(ctx, sub, in.ID)
		})
}
