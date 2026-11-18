package llm

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"
)

// SaveSettings 整组保存各用途的模型接口。
type SaveSettings struct {
	db    *bun.DB
	group *settings.Group[Settings]
}

// NewSaveSettings 返回保存模型接口设置的动作。
func NewSaveSettings(db *bun.DB, group *settings.Group[Settings]) *SaveSettings {
	return &SaveSettings{db: db, group: group}
}

// Handle 在一个事务中保存设置，有修改时记录操作日志。操作日志只记下改了哪些字段，不记录密钥。
func (a *SaveSettings) Handle(ctx context.Context, subject identity.Subject, s Settings) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed []string
		err := a.group.WithDB(tx).Update(ctx, func(cur *Settings) error {
			changed = changes(*cur, s)
			*cur = s
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
func (a *SaveSettings) HTTP(ctx context.Context, in *SettingsBody) (*SettingsBody, error) {
	subject, _ := identity.FromContext(ctx)
	if err := a.Handle(ctx, subject, in.Body); err != nil {
		return nil, err
	}
	return &SettingsBody{Body: in.Body}, nil
}
