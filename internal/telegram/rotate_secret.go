package telegram

import (
	"context"
	"log/slog"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// RotateSecret 更换 Telegram 渠道的 Webhook 鉴权密钥；直连模式下同步调用 setWebhook 刷新密钥，外部网关模式由网关侧换上新密钥。
type RotateSecret struct {
	db        *bun.DB
	crypt     *crypt.Crypt
	client    *Client
	publicURL string
}

// NewRotateSecret 返回更换 Webhook 密钥的动作。
func NewRotateSecret(db *bun.DB, c *crypt.Crypt, client *Client, publicURL string) *RotateSecret {
	if client == nil {
		client = NewClient(nil)
	}
	return &RotateSecret{db: db, crypt: c, client: client, publicURL: publicURL}
}

// Handle 为 sub 所在工作区的 Telegram 渠道 id 生成新的 Webhook 密钥并记录操作日志；直连且启用时先刷新 Telegram Webhook 后再落库提交。
func (a *RotateSecret) Handle(ctx context.Context, sub identity.Subject, id string) (*InstallationDTO, error) {
	nextSecret := newSecret()
	cur, err := find(ctx, a.db, sub.Tenant, id, false)
	if err != nil {
		return nil, err
	}
	curSettings, err := settingsOf(cur)
	if err != nil {
		return nil, err
	}
	if !curSettings.GatewayManaged && cur.Enabled {
		syncToken, err := a.crypt.Decrypt(curSettings.BotToken)
		if err != nil {
			return nil, errors.Wrap(err, "decrypt telegram bot token")
		}
		if err := a.client.SetWebhook(ctx, syncToken, webhookURL(a.publicURL, id), nextSecret); err != nil {
			slog.WarnContext(ctx, "telegram setWebhook failed before secret rotation", "channel_id", id, "error", err)
			return nil, err
		}
	}

	var ch *channel.Channel
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if ch, err = find(ctx, tx, sub.Tenant, id, true); err != nil {
			return err
		}
		ch.Settings["webhook_secret"] = a.crypt.Encrypt(nextSecret)
		if _, err := tx.NewUpdate().Model(ch).Column("settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "rotate telegram webhook secret")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.webhook_secret_rotated", TargetType: "channel", TargetID: id,
		})
	})
	if err != nil {
		return nil, err
	}
	return installation(ch, a.crypt, a.publicURL)
}

// HTTP 处理 POST /t/{slug}/api/telegrams/{id}/webhook-secret。
func (a *RotateSecret) HTTP(ctx context.Context, in *IDInput) (*InstallationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &InstallationOutput{Body: *dto}, nil
}
