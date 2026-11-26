package widget

import (
	"context"
)

// ConfigDTO 是聊天窗口打开之前读取的公开设置。
type ConfigDTO struct {
	Name       string     `json:"name" doc:"聊天插件的名称，显示在聊天窗口的标题中"`
	Appearance Appearance `json:"appearance" doc:"聊天窗口的外观配置"`
}

// ConfigOutput 是返回公开设置的响应。
type ConfigOutput struct {
	Body ConfigDTO
}

// GetConfig 读取客户在用的聊天插件的公开设置，公开接口无需登录。
type GetConfig struct{}

// NewGetConfig 返回读取公开设置的动作。
func NewGetConfig() *GetConfig { return &GetConfig{} }

// Handle 返回本次请求的聊天插件（Middleware 查出的）的名称与外观配置。聊天插件不存在、已停用或者
// 所在的工作区已停用时返回 channel.ErrNotFound。
func (a *GetConfig) Handle(ctx context.Context) (*ConfigDTO, error) {
	ch, err := channelFrom(ctx)
	if err != nil {
		return nil, err
	}
	s, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	return &ConfigDTO{Name: ch.Name, Appearance: s.appearance()}, nil
}

// HTTP 处理 GET /w/{channel}/api/config。
func (a *GetConfig) HTTP(ctx context.Context, _ *struct{}) (*ConfigOutput, error) {
	dto, err := a.Handle(ctx)
	if err != nil {
		return nil, err
	}
	return &ConfigOutput{Body: *dto}, nil
}
