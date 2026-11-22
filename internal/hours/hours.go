// Package hours 是工作区的工作时间：时区、每周的工作时间、特殊日期（节假日、调休）和通常的回复时间。
// 团队收件箱可以单独设置时区和每周的工作时间，特殊日期和回复时间沿用工作区的。
// 计算（是否在工作时间内、下次上班的时间）是纯函数，自动化、聊天插件和以后的 SLA 都用它。
package hours

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/settings"
)

var (
	// ErrInvalidTimeZone 是不认识的时区名。
	ErrInvalidTimeZone = errorx.Define(errorx.InvalidArgument, "hours.invalid_time_zone")
	// ErrInvalidInterval 是时间段的开始不早于结束。
	ErrInvalidInterval = errorx.Define(errorx.InvalidArgument, "hours.invalid_interval")
	// ErrOverlap 是同一天的两段时间重叠。
	ErrOverlap = errorx.Define(errorx.InvalidArgument, "hours.overlap")
	// ErrDuplicateDate 是两个特殊日期是同一天。
	ErrDuplicateDate = errorx.Define(errorx.InvalidArgument, "hours.duplicate_date")
)

// Interval 是一天中的一段工作时间，开始包括在内，结束不包括。时间是当地的钟点，结束可以是 24:00。
type Interval struct {
	Start string `json:"start" pattern:"^([01][0-9]|2[0-3]):[0-5][0-9]$" doc:"开始，例如 09:00"`
	End   string `json:"end" pattern:"^(([01][0-9]|2[0-3]):[0-5][0-9]|24:00)$" doc:"结束，例如 18:00；到当天结束写 24:00"`
}

// Week 是每周的工作时间：每天 0 到 3 段，没有时间段表示这天不上班。
type Week struct {
	Monday    []Interval `json:"monday" maxItems:"3" nullable:"false"`
	Tuesday   []Interval `json:"tuesday" maxItems:"3" nullable:"false"`
	Wednesday []Interval `json:"wednesday" maxItems:"3" nullable:"false"`
	Thursday  []Interval `json:"thursday" maxItems:"3" nullable:"false"`
	Friday    []Interval `json:"friday" maxItems:"3" nullable:"false"`
	Saturday  []Interval `json:"saturday" maxItems:"3" nullable:"false"`
	Sunday    []Interval `json:"sunday" maxItems:"3" nullable:"false"`
}

// days 返回 w 的七天：JSON 中的名字和这天的时间段，从周一开始。
func (w *Week) days() []struct {
	name string
	day  time.Weekday
	ivs  *[]Interval
} {
	type d = struct {
		name string
		day  time.Weekday
		ivs  *[]Interval
	}
	return []d{
		{"monday", time.Monday, &w.Monday}, {"tuesday", time.Tuesday, &w.Tuesday}, {"wednesday", time.Wednesday, &w.Wednesday},
		{"thursday", time.Thursday, &w.Thursday}, {"friday", time.Friday, &w.Friday}, {"saturday", time.Saturday, &w.Saturday},
		{"sunday", time.Sunday, &w.Sunday},
	}
}

// Schedule 是时区和每周的工作时间：工作区的，或者团队收件箱单独设置的。
type Schedule struct {
	TimeZone string `json:"time_zone" minLength:"1" maxLength:"64" doc:"IANA 时区名，例如 Asia/Shanghai；工作时间按这个时区的钟点计算"`
	Week     Week   `json:"week"`
}

// SpecialDate 是一个特殊日期：这天不按每周的工作时间。没有时间段表示全天不上班（节假日），有时间段表示这天按它们上班（例如调休）。
type SpecialDate struct {
	Date      string     `json:"date" format:"date" doc:"日期，例如 2026-10-01，按工作时间的时区"`
	Name      string     `json:"name" maxLength:"60" doc:"名称，例如 国庆节"`
	Intervals []Interval `json:"intervals" maxItems:"3" nullable:"false" doc:"这天的工作时间；为空表示全天不上班"`
}

// ReplyTime 是通常多久回复：告诉客户，也决定收件箱中等待时长的颜色。
type ReplyTime string

const (
	FewMinutes ReplyTime = "minutes" // 几分钟内
	WithinHour ReplyTime = "hour"    // 一小时内
	FewHours   ReplyTime = "hours"   // 几小时内
	WithinDay  ReplyTime = "day"     // 一天内
)

// ReplyTimes 是全部回复时间，显示文字是翻译文件中 hours.reply_time.<回复时间> 的消息。
var ReplyTimes = enum.New("hours.reply_time", FewMinutes, WithinHour, FewHours, WithinDay)

// Schema 让 OpenAPI 中的 ReplyTime 只接受上面的取值，并带有显示文字。
func (ReplyTime) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, ReplyTimes) }

// Duration 返回回复时间对应的时长：几分钟内是 5 分钟，几小时内是 4 小时。
func (r ReplyTime) Duration() time.Duration {
	switch r {
	case FewMinutes:
		return 5 * time.Minute
	case WithinHour:
		return time.Hour
	case FewHours:
		return 4 * time.Hour
	}
	return 24 * time.Hour
}

// alertFactor 是“需要注意”的时限相对于回复时间的倍数：等待超过回复时间是“等待处理”，超过它的 alertFactor 倍是“需要注意”。
const alertFactor = 3

// Settings 是工作区的工作时间设置，保存在 settings 中，每个工作区一组；没有保存过时是 Defaults。
// 字段不带 omitempty，见 settings 包的说明。
type Settings struct {
	Schedule
	SpecialDates []SpecialDate `json:"special_dates" nullable:"false"`
	ReplyTime    ReplyTime     `json:"reply_time"`
}

// nineToSix 是一天的 09:00–18:00。
var nineToSix = []Interval{{Start: "09:00", End: "18:00"}}

// Defaults 是新工作区的工作时间：上海时间周一到周五 09:00–18:00，没有特殊日期，几小时内回复。
var Defaults = Settings{
	Schedule: Schedule{TimeZone: "Asia/Shanghai", Week: Week{
		Monday: nineToSix, Tuesday: nineToSix, Wednesday: nineToSix, Thursday: nineToSix, Friday: nineToSix,
		Saturday: []Interval{}, Sunday: []Interval{},
	}},
	SpecialDates: []SpecialDate{},
	ReplyTime:    FewHours,
}

// NewGroup 返回工作时间的设置，名称 business_hours 保存在 settings 表中，发布后保持固定；用 Tenant 取某个工作区的。
func NewGroup(db bun.IDB) *settings.Group[Settings] {
	return settings.New(db, "business_hours", Defaults)
}

// clock 把 HH:MM 转换为一天中的分钟数，24:00 是 1440。格式已经由接口的 pattern 校验。
func clock(s string) int {
	var h, m int
	_, _ = fmt.Sscanf(s, "%d:%d", &h, &m)
	return h*60 + m
}

// validIntervals 检查一天的时间段：开始早于结束、彼此不重叠；错误指向 field 下的时间段。返回按开始排列的副本。
func validIntervals(field string, ivs []Interval) ([]Interval, error) {
	for i, iv := range ivs {
		if clock(iv.Start) >= clock(iv.End) {
			return nil, ErrInvalidInterval.New(errorx.Field(fmt.Sprintf("%s[%d].end", field, i)))
		}
	}
	sorted := append([]Interval{}, ivs...) // 空的天也是空列表，JSON 中是 []
	slices.SortFunc(sorted, func(x, y Interval) int { return clock(x.Start) - clock(y.Start) })
	for i := 1; i < len(sorted); i++ {
		if clock(sorted[i].Start) < clock(sorted[i-1].End) {
			j := slices.Index(ivs, sorted[i])
			return nil, ErrOverlap.New(errorx.Field(fmt.Sprintf("%s[%d].start", field, j)))
		}
	}
	return sorted, nil
}

// Normalize 检查 s 并整理：时区要认识，每天的时间段开始早于结束、不重叠，按开始排列；空的天写成空列表。
// 错误指向 prefix 下的字段，例如 prefix 为 "schedule." 时是 schedule.week.monday[1].end。
func (s *Schedule) Normalize(prefix string) error {
	if _, err := time.LoadLocation(s.TimeZone); err != nil || s.TimeZone == "Local" {
		return ErrInvalidTimeZone.New(errorx.Field(prefix+"time_zone"), errorx.Public("time_zone", s.TimeZone))
	}
	for _, d := range s.Week.days() {
		sorted, err := validIntervals(prefix+"week."+d.name, *d.ivs)
		if err != nil {
			return err
		}
		*d.ivs = sorted
	}
	return nil
}

// NormalizeSpecialDates 检查特殊日期并整理：日期不重复，时间段开始早于结束、不重叠；按日期排列。错误指向 prefix 下的字段。
func NormalizeSpecialDates(prefix string, dates []SpecialDate) ([]SpecialDate, error) {
	seen := map[string]bool{}
	out := make([]SpecialDate, len(dates))
	for i, d := range dates {
		if seen[d.Date] {
			return nil, ErrDuplicateDate.New(errorx.Field(fmt.Sprintf("%s[%d].date", prefix, i)), errorx.Public("date", d.Date))
		}
		seen[d.Date] = true
		ivs, err := validIntervals(fmt.Sprintf("%s[%d].intervals", prefix, i), d.Intervals)
		if err != nil {
			return nil, err
		}
		d.Intervals = ivs
		out[i] = d
	}
	slices.SortFunc(out, func(x, y SpecialDate) int {
		if x.Date < y.Date {
			return -1
		}
		if x.Date > y.Date {
			return 1
		}
		return 0
	})
	return out, nil
}

// Hours 是一套可以计算的工作时间：时区、每周的工作时间和特殊日期。由 Compile 或 For 得到。
type Hours struct {
	loc     *time.Location
	week    Week
	special map[string][]Interval // 日期（2006-01-02）→ 这天的时间段
}

// Compile 由时间表 s 和特殊日期 dates 得到可以计算的工作时间。s 已经由 Normalize 检查过。
func Compile(s Schedule, dates []SpecialDate) (Hours, error) {
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		return Hours{}, errors.Wrapf(err, "load time zone %q", s.TimeZone)
	}
	special := make(map[string][]Interval, len(dates))
	for _, d := range dates {
		special[d.Date] = d.Intervals
	}
	return Hours{loc: loc, week: s.Week, special: special}, nil
}

// on 返回日期 y-m-d（当地）的时间段：特殊日期优先，否则按星期几。
func (h Hours) on(day time.Time) []Interval {
	if ivs, ok := h.special[day.Format(time.DateOnly)]; ok {
		return ivs
	}
	w := h.week
	for _, d := range w.days() {
		if d.day == day.Weekday() {
			return *d.ivs
		}
	}
	return nil
}

// TimeZone 返回工作时间 h 使用的 IANA 时区名。
func (h Hours) TimeZone() string { return h.loc.String() }

// OpenAt 报告时刻 t 是否在工作时间内：按 t 在工作时间时区中的钟点，落在这天的某段时间内（包括开始，不包括结束）。
func (h Hours) OpenAt(t time.Time) bool {
	local := t.In(h.loc)
	sec := local.Hour()*3600 + local.Minute()*60 + local.Second()
	for _, iv := range h.on(local) {
		if clock(iv.Start)*60 <= sec && sec < clock(iv.End)*60 {
			return true
		}
	}
	return false
}

// NextOpen 返回 t 之后第一次上班的时刻；t 在工作时间内时返回 t。一年之内都不上班时 ok 为 false。
// 钟点在夏令时跳过的时段中时，按 time.Date 的规则顺延。
func (h Hours) NextOpen(t time.Time) (next time.Time, ok bool) {
	if h.OpenAt(t) {
		return t, true
	}
	local := t.In(h.loc)
	for offset := 0; offset <= 366; offset++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+offset, 12, 0, 0, 0, h.loc)
		for _, iv := range h.on(day) {
			m := clock(iv.Start)
			at := time.Date(day.Year(), day.Month(), day.Day(), m/60, m%60, 0, 0, h.loc)
			if at.After(t) {
				return at, true
			}
		}
	}
	return time.Time{}, false
}

// For 返回工作区 tenant 中团队收件箱 teamID 实际使用的工作时间：团队收件箱单独设置了时区和每周的工作时间时用它的，
// 否则用工作区的；特殊日期总是工作区的。teamID 为空时是工作区的。
func For(ctx context.Context, db bun.IDB, tenant string, teamID *string) (Hours, error) {
	s, err := NewGroup(db).Tenant(tenant).Get(ctx)
	if err != nil {
		return Hours{}, err
	}
	schedule := s.Schedule
	if teamID != nil {
		var own *Schedule
		err := db.NewSelect().Table("teams").Column("business_hours").Where("id = ? AND tenant_id = ?", *teamID, tenant).Scan(ctx, &own)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Hours{}, errors.Wrap(err, "load team business hours")
		}
		if own != nil {
			schedule = *own
		}
	}
	return Compile(schedule, s.SpecialDates)
}
