package telegram

import (
	"context"
	"log/slog"
	"maps"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// UpdateBody 是修改 Telegram 机器人渠道的请求体；BotToken 留空表示保持原有 Token 不变。
type UpdateBody struct {
	Name           string  `json:"name" minLength:"1" maxLength:"60"`
	TeamID         *string `json:"team_id,omitempty" format:"uuid" doc:"新会话进入的团队收件箱；不填时进入未指派"`
	BotToken       string  `json:"bot_token,omitempty" maxLength:"256" format:"password" doc:"新的 Bot Token；留空则保持原 Token 不变"`
	GatewayManaged bool    `json:"gateway_managed" doc:"开启后由外部业务网关转发 Webhook，系统不自动调用 setWebhook"`
	Enabled        bool    `json:"enabled" doc:"停用后不再接收或发送消息"`
}

// UpdateInput 是修改 Telegram 渠道的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateBody
}

// Update 修改当前工作区中的一个 Telegram 机器人渠道，并在直连模式下同步 Telegram Webhook 状态。
type Update struct {
	db        *bun.DB
	crypt     *crypt.Crypt
	client    *Client
	publicURL string
}

// NewUpdate 返回修改 Telegram 渠道的动作。
func NewUpdate(db *bun.DB, c *crypt.Crypt, client *Client, publicURL string) *Update {
	if client == nil {
		client = NewClient(nil)
	}
	return &Update{db: db, crypt: c, client: client, publicURL: publicURL}
}

// Handle 修改 sub 所在工作区中的 Telegram 渠道 id 并记录操作日志。
// 若提供了新 BotToken 会先经 getMe 校验；直连模式下按启用状态自动同步 setWebhook 或 deleteWebhook，外部网关转发模式不触碰 Telegram Webhook。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, b UpdateBody) (*TelegramDTO, error) {
	newToken := strings.TrimSpace(b.BotToken)
	var newUsername string
	if newToken != "" {
		var err error
		if newUsername, err = a.client.GetMe(ctx, newToken); err != nil {
			return nil, err
		}
	}
	var syncAction string // "set" | "delete" | ""
	var syncToken, syncSecret string

	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if !sameTeam(ch.TeamID, b.TeamID) {
			if err := checkTeam(ctx, tx, sub.Tenant, b.TeamID); err != nil {
				return err
			}
		}
		s, err := settingsOf(ch)
		if err != nil {
			return err
		}
		var changed []string
		if ch.Name != b.Name {
			changed = append(changed, "name")
		}
		if !sameTeam(ch.TeamID, b.TeamID) {
			changed = append(changed, "team_id")
		}
		enabledChanged := ch.Enabled != b.Enabled
		if enabledChanged {
			changed = append(changed, "enabled")
		}
		modeChanged := s.GatewayManaged != b.GatewayManaged
		if modeChanged || newToken != "" {
			changed = append(changed, "settings")
		}
		if len(changed) == 0 {
			return nil
		}
		if newToken != "" {
			s.BotToken = a.crypt.Encrypt(newToken)
			s.BotUsername = newUsername
		}
		s.GatewayManaged = b.GatewayManaged
		settings, err := s.toMap()
		if err != nil {
			return err
		}
		maps.Copy(ch.Settings, settings)
		ch.Name, ch.TeamID, ch.Enabled = b.Name, b.TeamID, b.Enabled
		if _, err := tx.NewUpdate().Model(ch).Column("name", "team_id", "enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update telegram channel")
		}
		// 直连模式下：启用态且（首次切直连、重新启用或换 Token）时同步 setWebhook；停用时清理 Webhook。
		if !s.GatewayManaged && (enabledChanged || modeChanged || newToken != "") {
			plainToken, err := a.crypt.Decrypt(s.BotToken)
			if err != nil {
				return errors.Wrap(err, "decrypt telegram bot token")
			}
			syncToken = plainToken
			if b.Enabled {
				plainSecret, err := a.crypt.Decrypt(s.WebhookSecret)
				if err != nil {
					return errors.Wrap(err, "decrypt telegram webhook secret")
				}
				syncSecret = plainSecret
				syncAction = "set"
			} else {
				syncAction = "delete"
			}
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.updated", TargetType: "channel", TargetID: id, Properties: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	switch syncAction {
	case "set":
		if err := a.client.SetWebhook(ctx, syncToken, webhookURL(a.publicURL, id), syncSecret); err != nil {
			slog.WarnContext(ctx, "telegram setWebhook failed on update", "channel_id", id, "error", err)
			return nil, err
		}
	case "delete":
		if err := a.client.DeleteWebhook(ctx, syncToken); err != nil {
			slog.WarnContext(ctx, "telegram deleteWebhook failed on disable", "channel_id", id, "error", err)
			return nil, err
		}
	}
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 PUT /t/{slug}/api/telegrams/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*TelegramOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &TelegramOutput{Body: *dto}, nil
}
