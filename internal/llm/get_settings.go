package llm

import (
	"context"

	"github.com/go-tenon/tenon/settings"
)

// GetSettings 读取各调用类型的模型接口，供管理员端显示和修改。
type GetSettings struct {
	group *settings.Group[Settings]
}

// NewGetSettings 返回读取模型接口设置的动作。
func NewGetSettings(group *settings.Group[Settings]) *GetSettings {
	return &GetSettings{group: group}
}

// HTTP 处理 GET /settings/model。
func (a *GetSettings) HTTP(ctx context.Context, _ *struct{}) (*SettingsBody, error) {
	s, err := a.group.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &SettingsBody{Body: s}, nil
}
