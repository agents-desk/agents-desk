package telegram

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// CreateBody 是新建 Telegram 机器人渠道的请求体。
type CreateBody struct {
	Name           string           `json:"name" minLength:"1" maxLength:"60"`
	Audience       channel.Audience `json:"audience,omitempty" doc:"服务对象类型：customer（外部客户，默认）或 internal（内部员工）"`
	TeamID         *string          `json:"team_id,omitempty" format:"uuid" doc:"新会话进入的团队收件箱；不填时进入未指派"`
	BotToken       string           `json:"bot_token" minLength:"10" maxLength:"256" format:"password" doc:"从 @BotFather 获取的 Bot Token"`
	GatewayManaged bool             `json:"gateway_managed,omitempty" doc:"开启后由外部业务网关转发 Webhook，系统不自动调用 setWebhook"`
	EnableGroups   *bool            `json:"enable_groups,omitempty" doc:"是否接收群内 @ 机器人与命令消息；不填默认启用"`
}

// CreateInput 是新建 Telegram 渠道的 HTTP 请求。
type CreateInput struct {
	Body CreateBody
}

// Create 在当前工作区新建一个 Telegram 机器人渠道，校验 Bot Token 并生成 Webhook 鉴权密钥。
type Create struct {
	db        *bun.DB
	crypt     *crypt.Crypt
	client    *Client
	publicURL string
}

// NewCreate 返回新建 Telegram 渠道的动作；crypt 加密保存敏感凭据，client 调用 Telegram Bot API，publicURL 用于直连模式设置 Webhook。
func NewCreate(db *bun.DB, c *crypt.Crypt, client *Client, publicURL string) *Create {
	if client == nil {
		client = NewClient(nil)
	}
	return &Create{db: db, crypt: c, client: client, publicURL: publicURL}
}

// Handle 校验 Bot Token 后在 sub 所在工作区新建启用的 Telegram 渠道；直连模式下自动设置 Webhook，设置失败时回滚清理并返回 ErrWebhookFailed。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, b CreateBody) (*TelegramDTO, error) {
	token := strings.TrimSpace(b.BotToken)
	me, err := a.client.GetMe(ctx, token)
	if err != nil {
		return nil, err
	}
	audience := normalizeBotAudience(b.Audience, channel.AudienceCustomer)
	enableGroups := true
	if b.EnableGroups != nil {
		enableGroups = *b.EnableGroups
	}
	secret := newSecret()
	settings, err := Settings{
		BotID:          me.ID,
		BotToken:       a.crypt.Encrypt(token),
		BotUsername:    me.Username,
		WebhookSecret:  a.crypt.Encrypt(secret),
		GatewayManaged: b.GatewayManaged,
		EnableGroups:   &enableGroups,
	}.toMap()
	if err != nil {
		return nil, err
	}
	id := newID()
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := checkTeam(ctx, tx, sub.Tenant, b.TeamID); err != nil {
			return err
		}
		ch := &channel.Channel{
			ID: id, TenantID: sub.Tenant, Kind: channel.Telegram, Audience: audience, Name: b.Name,
			Settings: settings, TeamID: b.TeamID, Enabled: true,
		}
		if _, err := tx.NewInsert().Model(ch).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert telegram channel")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.created", TargetType: "channel", TargetID: id,
			Properties: map[string]any{"kind": string(channel.Telegram), "audience": string(audience)},
		})
	})
	if err != nil {
		return nil, err
	}
	if !b.GatewayManaged {
		if err := a.client.SetWebhook(ctx, token, webhookURL(a.publicURL, id), secret); err != nil {
			slog.WarnContext(ctx, "telegram setWebhook failed on create, rolling back channel", "channel_id", id, "error", err)
			if _, delErr := a.db.NewDelete().Model((*channel.Channel)(nil)).Where("id = ? AND tenant_id = ?", id, sub.Tenant).Exec(ctx); delErr != nil {
				slog.ErrorContext(ctx, "rollback telegram channel after setWebhook failure", "channel_id", id, "error", delErr)
			}
			return nil, err
		}
	}
	slog.InfoContext(ctx, "telegram channel created", "channel_id", id, "bot_username", me.Username, "audience", audience, "gateway_managed", b.GatewayManaged)
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 POST /t/{slug}/api/telegrams。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*TelegramOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TelegramOutput{Body: *dto}, nil
}
