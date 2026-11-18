package llm

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/settings"
)

// GetSettings 读取模型接口设置，供管理员端显示。
type GetSettings struct {
	group *settings.Group[Settings]
	crypt *crypt.Crypt
}

// NewGetSettings 返回读取模型接口设置的动作。
func NewGetSettings(group *settings.Group[Settings], c *crypt.Crypt) *GetSettings {
	return &GetSettings{group: group, crypt: c}
}

// Handle 返回设置的显示形式：密钥解密后只取提示。
func (a *GetSettings) Handle(ctx context.Context) (View, error) {
	s, err := a.group.Get(ctx)
	if err != nil {
		return View{}, err
	}
	return view(a.crypt, s)
}

// HTTP 处理 GET /settings/model。
func (a *GetSettings) HTTP(ctx context.Context, _ *struct{}) (*ViewOutput, error) {
	v, err := a.Handle(ctx)
	if err != nil {
		return nil, err
	}
	return &ViewOutput{Body: v}, nil
}

// view 把保存的设置转换为显示形式。密钥无法解密时（应用密钥丢失或被换掉）返回错误。
func view(c *crypt.Crypt, s Settings) (View, error) {
	v := View{BaseURL: s.BaseURL, Models: s.Models}
	if s.APIKeyEncrypted != "" {
		key, err := c.Decrypt(s.APIKeyEncrypted)
		if err != nil {
			return View{}, errors.Wrap(err, "decrypt model api key")
		}
		v.APIKeySet, v.APIKeyHint = true, hint(key)
	}
	return v, nil
}
