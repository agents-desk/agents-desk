package member

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// GetMyStatus 读取当前成员在这个工作区中的在线状态。
type GetMyStatus struct {
	db *bun.DB
}

// NewGetMyStatus 返回读取自己的在线状态的动作。
func NewGetMyStatus(db *bun.DB) *GetMyStatus { return &GetMyStatus{db: db} }

// Handle 返回 sub 在他所在工作区（sub.Tenant）中的在线状态，没有设置过时是离开。
func (a *GetMyStatus) Handle(ctx context.Context, sub identity.Subject) (*StatusSetting, error) {
	s, err := statusOf(ctx, a.db, sub.Tenant, sub.ID, false)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// HTTP 处理 GET /t/{slug}/api/me/status。
func (a *GetMyStatus) HTTP(ctx context.Context, _ *struct{}) (*StatusSettingOutput, error) {
	sub, _ := identity.FromContext(ctx)
	s, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &StatusSettingOutput{Body: *s}, nil
}
