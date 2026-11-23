package telegram

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// InstallationDTO 是配置 Telegram 渠道或外部网关转发所需的接入参数。
type InstallationDTO struct {
	ChannelID      string `json:"channel_id" doc:"Telegram 渠道 ID"`
	BotUsername    string `json:"bot_username" doc:"Telegram 机器人用户名（不含 @）"`
	GatewayManaged bool   `json:"gateway_managed" doc:"是否由外部业务网关转发 Webhook"`
	WebhookURL     string `json:"webhook_url" doc:"接收 Telegram Update 的 Webhook 地址"`
	WebhookSecret  string `json:"webhook_secret" doc:"Webhook 鉴权密钥，放入 X-Telegram-Bot-Api-Secret-Token 请求头"`
}

// InstallationOutput 是返回 Telegram 接入参数的响应。
type InstallationOutput struct {
	Body InstallationDTO
}

// GetInstallation 读取 Telegram 渠道的 Webhook 地址与鉴权密钥。
type GetInstallation struct {
	db        *bun.DB
	crypt     *crypt.Crypt
	publicURL string
}

// NewGetInstallation 返回读取 Telegram 接入参数的动作。
func NewGetInstallation(db *bun.DB, c *crypt.Crypt, publicURL string) *GetInstallation {
	return &GetInstallation{db: db, crypt: c, publicURL: publicURL}
}

// Handle 返回 sub 所在工作区中 Telegram 渠道 id 的接入参数，不存在时返回 channel.ErrNotFound。
func (a *GetInstallation) Handle(ctx context.Context, sub identity.Subject, id string) (*InstallationDTO, error) {
	ch, err := find(ctx, a.db, sub.Tenant, id, false)
	if err != nil {
		return nil, err
	}
	return installation(ch, a.crypt, a.publicURL)
}

// HTTP 处理 GET /t/{slug}/api/telegrams/{id}/installation。
func (a *GetInstallation) HTTP(ctx context.Context, in *IDInput) (*InstallationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &InstallationOutput{Body: *dto}, nil
}

func installation(ch *channel.Channel, c *crypt.Crypt, publicURL string) (*InstallationDTO, error) {
	s, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	secret, err := c.Decrypt(s.WebhookSecret)
	if err != nil {
		return nil, err
	}
	return &InstallationDTO{
		ChannelID:      ch.ID,
		BotUsername:    s.BotUsername,
		GatewayManaged: s.GatewayManaged,
		WebhookURL:     webhookURL(publicURL, ch.ID),
		WebhookSecret:  secret,
	}, nil
}
