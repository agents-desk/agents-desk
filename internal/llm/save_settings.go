package llm

import (
	"context"
	"slices"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"
)

// Changes 是提交的模型接口设置。密钥只写不读：APIKey 为空时保持原来的密钥，ClearAPIKey 为 true 时删除它。
type Changes struct {
	BaseURL     string `json:"base_url" maxLength:"500" pattern:"^(https?://\\S+)?$" doc:"OpenAI 兼容接口的地址，http:// 或 https:// 开头；为空表示不使用模型"`
	APIKey      string `json:"api_key,omitempty" maxLength:"500" doc:"新的密钥；为空时保持原来的密钥"`
	ClearAPIKey bool   `json:"clear_api_key,omitempty" doc:"删除已保存的密钥"`
	Models      Models `json:"models"`
}

// SaveInput 是保存设置的请求。
type SaveInput struct {
	Body Changes
}

// SaveSettings 保存模型接口设置，密钥用应用密钥加密后保存。
type SaveSettings struct {
	db    *bun.DB
	group *settings.Group[Settings]
	crypt *crypt.Crypt
}

// NewSaveSettings 返回保存模型接口设置的动作。
func NewSaveSettings(db *bun.DB, group *settings.Group[Settings], c *crypt.Crypt) *SaveSettings {
	return &SaveSettings{db: db, group: group, crypt: c}
}

// Handle 在一个事务中修改设置并记录操作日志。操作日志只记下改了哪些项，不记录地址、模型名以外的值，密钥只记“已修改”。
func (a *SaveSettings) Handle(ctx context.Context, subject identity.Subject, c Changes) (View, error) {
	var saved Settings
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed []string
		err := a.group.WithDB(tx).Update(ctx, func(s *Settings) error {
			if s.BaseURL != c.BaseURL {
				changed = append(changed, "base_url")
			}
			if s.Models != c.Models {
				changed = append(changed, "models")
			}
			s.BaseURL, s.Models = c.BaseURL, c.Models
			switch {
			case c.ClearAPIKey:
				if s.APIKeyEncrypted != "" {
					changed = append(changed, "api_key")
				}
				s.APIKeyEncrypted = ""
			case c.APIKey != "":
				changed = append(changed, "api_key")
				s.APIKeyEncrypted = a.crypt.Encrypt(c.APIKey)
			}
			saved = *s
			return nil
		})
		if err != nil || len(changed) == 0 {
			return err
		}
		props := map[string]any{"changed": changed}
		if slices.Contains(changed, "base_url") {
			props["base_url"] = c.BaseURL
		}
		if slices.Contains(changed, "models") {
			props["models"] = c.Models
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "platform_settings.changed", TargetType: "settings", TargetID: "llm", Properties: props,
		})
	})
	if err != nil {
		return View{}, err
	}
	return view(a.crypt, saved)
}

// HTTP 处理 PUT /settings/model。
func (a *SaveSettings) HTTP(ctx context.Context, in *SaveInput) (*ViewOutput, error) {
	subject, _ := identity.FromContext(ctx)
	v, err := a.Handle(ctx, subject, in.Body)
	if err != nil {
		return nil, err
	}
	return &ViewOutput{Body: v}, nil
}
