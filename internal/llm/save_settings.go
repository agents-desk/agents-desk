package llm

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"
)

// SaveSettingsInput 是保存模型网关和各调用类型模型设置的请求。
type SaveSettingsInput struct {
	Body Settings
}

// SaveSettings 保存平台的统一模型网关连接与各调用类型模型名。
type SaveSettings struct {
	db    *bun.DB
	group *settings.Group[Settings]
}

// NewSaveSettings 返回保存模型设置的动作。
func NewSaveSettings(db *bun.DB, group *settings.Group[Settings]) *SaveSettings {
	return &SaveSettings{db: db, group: group}
}

// Handle 在一个事务中更新模型设置，有字段变更时记录操作日志；操作日志只记字段名，不记录密钥明文。
func (a *SaveSettings) Handle(ctx context.Context, subject identity.Subject, updated Settings) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed []string
		err := a.group.WithDB(tx).Update(ctx, func(s *Settings) error {
			changed = a.group.Diff(*s, updated)
			*s = updated
			return nil
		})
		if err != nil || len(changed) == 0 {
			return err
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "platform_settings.changed", TargetType: "settings", TargetID: "llm",
			Properties: map[string]any{"changed": changed},
		})
	})
}

// HTTP 处理 PUT /settings/model。
func (a *SaveSettings) HTTP(ctx context.Context, in *SaveSettingsInput) (*SettingsBody, error) {
	subject, _ := identity.FromContext(ctx)
	if err := a.Handle(ctx, subject, in.Body); err != nil {
		return nil, err
	}
	return &SettingsBody{Body: in.Body}, nil
}
