package team

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// SetDisabled 停用或启用团队收件箱。停用后它停止接收新会话和自动分配，从收件箱侧栏隐藏；历史会话和报表保留它的名称。
type SetDisabled struct {
	db       *bun.DB
	disabled bool // true 是停用，false 是启用
}

// NewDisable 返回停用团队收件箱的动作。
func NewDisable(db *bun.DB) *SetDisabled { return &SetDisabled{db: db, disabled: true} }

// NewEnable 返回启用团队收件箱的动作。
func NewEnable(db *bun.DB) *SetDisabled { return &SetDisabled{db: db, disabled: false} }

// Handle 停用或启用 sub 所在工作区中的团队收件箱 id 并记录操作日志。已经是目标状态时什么也不做。不存在时返回 ErrNotFound。
func (a *SetDisabled) Handle(ctx context.Context, sub identity.Subject, id string) (*TeamDTO, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if (t.DisabledAt != nil) == a.disabled {
			return nil
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		t.DisabledAt, t.UpdatedAt = nil, at
		event := "team.enabled"
		if a.disabled {
			t.DisabledAt, event = &at, "team.disabled"
		}
		if _, err := tx.NewUpdate().Model(t).Column("disabled_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update team status")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: event, TargetType: "team", TargetID: t.ID, Properties: map[string]any{"name": t.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 POST /t/{slug}/api/teams/{id}/disable 和 POST /t/{slug}/api/teams/{id}/enable。
func (a *SetDisabled) HTTP(ctx context.Context, in *IDInput) (*TeamOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &TeamOutput{Body: *dto}, nil
}
