package team

import (
	"context"
	"reflect"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/hours"
)

// BusinessHoursInput 是设置团队收件箱的工作时间的请求。
type BusinessHoursInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Schedule *hours.Schedule `json:"schedule,omitempty" doc:"单独设置的时区和每周的工作时间；不传表示使用工作区的工作时间"`
	}
}

// SetBusinessHours 设置团队收件箱的工作时间：单独设置时区和每周的工作时间，或者使用工作区的。特殊日期和回复时间总是工作区的。
type SetBusinessHours struct {
	db *bun.DB
}

// NewSetBusinessHours 返回设置团队收件箱工作时间的动作。
func NewSetBusinessHours(db *bun.DB) *SetBusinessHours { return &SetBusinessHours{db: db} }

// Handle 把 sub 所在工作区中团队收件箱 id 的工作时间设为 s（为空表示使用工作区的），有变化时记录操作日志。
// 团队收件箱不存在时返回 ErrNotFound；s 的错误见 hours.Schedule.Normalize，指向 schedule 下的字段。
func (a *SetBusinessHours) Handle(ctx context.Context, sub identity.Subject, id string, s *hours.Schedule) (*TeamDTO, error) {
	if s != nil {
		if err := s.Normalize("schedule."); err != nil {
			return nil, err
		}
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if reflect.DeepEqual(t.BusinessHours, s) {
			return nil
		}
		t.BusinessHours = s
		if _, err := tx.NewUpdate().Model(t).Column("business_hours", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update team business hours")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "team.updated", TargetType: "team", TargetID: id, Properties: map[string]any{"changed": []string{"business_hours"}},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 PUT /t/{slug}/api/teams/{id}/business-hours。
func (a *SetBusinessHours) HTTP(ctx context.Context, in *BusinessHoursInput) (*TeamOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.Schedule)
	if err != nil {
		return nil, err
	}
	return &TeamOutput{Body: *dto}, nil
}
