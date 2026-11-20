package hours

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// SettingsDTO 是接口中的工作时间设置，带有收件箱中等待时长变色的时限。
type SettingsDTO struct {
	Settings
	WarnAfter  int `json:"warn_after" doc:"等待回复超过这么多秒标黄（等待处理）：等于回复时间"`
	AlertAfter int `json:"alert_after" doc:"等待回复超过这么多秒标深橙（需要注意）"`
}

// SettingsOutput 是返回工作时间设置的响应。
type SettingsOutput struct {
	Body SettingsDTO
}

// toDTO 返回 s 在接口中的形式。
func toDTO(s Settings) SettingsDTO {
	warn := s.ReplyTime.Duration()
	return SettingsDTO{Settings: s, WarnAfter: int(warn.Seconds()), AlertAfter: int((warn * alertFactor).Seconds())}
}

// Get 读取当前工作区的工作时间设置。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取工作时间设置的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 返回 sub 所在工作区的工作时间设置，没有保存过时是 Defaults。
func (a *Get) Handle(ctx context.Context, sub identity.Subject) (*SettingsDTO, error) {
	s, err := NewGroup(a.db).Tenant(sub.Tenant).Get(ctx)
	if err != nil {
		return nil, err
	}
	dto := toDTO(s)
	return &dto, nil
}

// HTTP 处理 GET /t/{slug}/api/settings/business-hours。
func (a *Get) HTTP(ctx context.Context, _ *struct{}) (*SettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &SettingsOutput{Body: *dto}, nil
}
