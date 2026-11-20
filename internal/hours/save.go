package hours

import (
	"context"
	"reflect"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// Part 是工作时间设置中单独保存的一部分，设置页的每个标签对应一部分。
type Part string

const (
	SchedulePart     Part = "schedule"      // 时区和每周的工作时间
	SpecialDatesPart Part = "special_dates" // 特殊日期
	ReplyTimePart    Part = "reply_time"    // 回复时间
)

// ScheduleInput 是保存时区和每周的工作时间的请求。
type ScheduleInput struct {
	Body Schedule
}

// SpecialDatesInput 是保存特殊日期的请求。
type SpecialDatesInput struct {
	Body struct {
		SpecialDates []SpecialDate `json:"special_dates" maxItems:"100" nullable:"false"`
	}
}

// ReplyTimeInput 是保存回复时间的请求。
type ReplyTimeInput struct {
	Body struct {
		ReplyTime ReplyTime `json:"reply_time"`
	}
}

// Save 保存当前工作区工作时间设置中的一部分，其他部分保持原样。
type Save struct {
	db *bun.DB
}

// NewSave 返回保存工作时间设置的动作。
func NewSave(db *bun.DB) *Save { return &Save{db: db} }

// Handle 在一个事务中锁住 sub 所在工作区的工作时间设置，用 change 修改其中的 part，有变化时记录操作日志。
// change 返回的错误（校验失败）原样返回，设置保持原样。
func (a *Save) Handle(ctx context.Context, sub identity.Subject, part Part, change func(*Settings) error) (*SettingsDTO, error) {
	var saved Settings
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed bool
		err := NewGroup(tx).Tenant(sub.Tenant).Update(ctx, func(s *Settings) error {
			before := *s
			if err := change(s); err != nil {
				return err
			}
			changed, saved = !reflect.DeepEqual(before, *s), *s
			return nil
		})
		if err != nil || !changed {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "tenant_settings.changed", TargetType: "settings", TargetID: "business_hours",
			Properties: map[string]any{"part": string(part)},
		})
	})
	if err != nil {
		return nil, err
	}
	dto := toDTO(saved)
	return &dto, nil
}

// SaveSchedule 保存 sub 所在工作区的时区和每周的工作时间，检查和整理见 Schedule.Normalize。
func (a *Save) SaveSchedule(ctx context.Context, sub identity.Subject, s Schedule) (*SettingsDTO, error) {
	if err := s.Normalize(""); err != nil {
		return nil, err
	}
	return a.Handle(ctx, sub, SchedulePart, func(cur *Settings) error { cur.Schedule = s; return nil })
}

// SaveSpecialDates 保存 sub 所在工作区的特殊日期，检查和整理见 NormalizeSpecialDates。
func (a *Save) SaveSpecialDates(ctx context.Context, sub identity.Subject, dates []SpecialDate) (*SettingsDTO, error) {
	dates, err := NormalizeSpecialDates("special_dates", dates)
	if err != nil {
		return nil, err
	}
	return a.Handle(ctx, sub, SpecialDatesPart, func(cur *Settings) error { cur.SpecialDates = dates; return nil })
}

// SaveReplyTime 保存 sub 所在工作区的回复时间。
func (a *Save) SaveReplyTime(ctx context.Context, sub identity.Subject, r ReplyTime) (*SettingsDTO, error) {
	return a.Handle(ctx, sub, ReplyTimePart, func(cur *Settings) error { cur.ReplyTime = r; return nil })
}

// ScheduleHTTP 处理 PUT /t/{slug}/api/settings/business-hours/schedule。
func (a *Save) ScheduleHTTP(ctx context.Context, in *ScheduleInput) (*SettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	return output(a.SaveSchedule(ctx, sub, in.Body))
}

// SpecialDatesHTTP 处理 PUT /t/{slug}/api/settings/business-hours/special-dates。
func (a *Save) SpecialDatesHTTP(ctx context.Context, in *SpecialDatesInput) (*SettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	return output(a.SaveSpecialDates(ctx, sub, in.Body.SpecialDates))
}

// ReplyTimeHTTP 处理 PUT /t/{slug}/api/settings/business-hours/reply-time。
func (a *Save) ReplyTimeHTTP(ctx context.Context, in *ReplyTimeInput) (*SettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	return output(a.SaveReplyTime(ctx, sub, in.Body.ReplyTime))
}

// output 把保存的结果包装成响应。
func output(dto *SettingsDTO, err error) (*SettingsOutput, error) {
	if err != nil {
		return nil, err
	}
	return &SettingsOutput{Body: *dto}, nil
}
