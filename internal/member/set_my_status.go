package member

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// SetMyStatusInput 是修改自己的在线状态的 HTTP 请求。
type SetMyStatusInput struct {
	Body StatusSetting
}

// SetMyStatus 修改当前成员在这个工作区中的在线状态。
type SetMyStatus struct {
	db *bun.DB
}

// NewSetMyStatus 返回修改自己的在线状态的动作。
func NewSetMyStatus(db *bun.DB) *SetMyStatus { return &SetMyStatus{db: db} }

// Handle 把 sub 在他所在工作区（sub.Tenant）中的在线状态改为 s，有变化时记录操作日志。
func (a *SetMyStatus) Handle(ctx context.Context, sub identity.Subject, s StatusSetting) (*StatusSetting, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		current, err := statusOf(ctx, tx, sub.Tenant, sub.ID, true)
		if err != nil || current == s {
			return err
		}
		if err := saveStatus(ctx, tx, sub.Tenant, sub.ID, s); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.status_changed", TargetType: string(Type), TargetID: sub.ID,
			Properties: map[string]any{"status": s.Status, "reassign": s.Reassign},
		})
	})
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// HTTP 处理 PUT /t/{slug}/api/me/status。
func (a *SetMyStatus) HTTP(ctx context.Context, in *SetMyStatusInput) (*StatusSettingOutput, error) {
	sub, _ := identity.FromContext(ctx)
	s, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &StatusSettingOutput{Body: *s}, nil
}
