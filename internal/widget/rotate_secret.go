package widget

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// RotateSecret 更换聊天插件的身份验证密钥：旧密钥签名的用户身份随即失效，已经建立的会话不受影响。
type RotateSecret struct {
	db        *bun.DB
	crypt     *crypt.Crypt
	publicURL string
}

// NewRotateSecret 返回更换身份验证密钥的动作；crypt 加密保存新密钥，publicURL 用来返回新的安装内容。
func NewRotateSecret(db *bun.DB, c *crypt.Crypt, publicURL string) *RotateSecret {
	return &RotateSecret{db: db, crypt: c, publicURL: publicURL}
}

// Handle 为 sub 所在工作区中的聊天插件 id 生成新的身份验证密钥，记录操作日志，返回新的安装内容。
// 聊天插件不存在时返回 channel.ErrNotFound。
func (a *RotateSecret) Handle(ctx context.Context, sub identity.Subject, id string) (*InstallationDTO, error) {
	var ch *channel.Channel
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if ch, err = find(ctx, tx, sub.Tenant, id, true); err != nil {
			return err
		}
		ch.Settings["identity_secret"] = a.crypt.Encrypt(newSecret())
		if _, err := tx.NewUpdate().Model(ch).Column("settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "rotate identity secret")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.identity_secret_rotated", TargetType: "channel", TargetID: id,
		})
	})
	if err != nil {
		return nil, err
	}
	return installation(ch, a.crypt, a.publicURL)
}

// HTTP 处理 POST /t/{slug}/api/widgets/{id}/identity-secret。
func (a *RotateSecret) HTTP(ctx context.Context, in *IDInput) (*InstallationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &InstallationOutput{Body: *dto}, nil
}
