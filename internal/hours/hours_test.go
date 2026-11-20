package hours_test

import (
	"testing"
	"time"

	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/hours"
)

// compile 返回时区 tz、每周工作时间 week 和特殊日期 dates 的工作时间。
func compile(t *testing.T, tz string, week hours.Week, dates ...hours.SpecialDate) hours.Hours {
	t.Helper()
	s := hours.Schedule{TimeZone: tz, Week: week}
	if err := s.Normalize(""); err != nil {
		t.Fatal(err)
	}
	h, err := hours.Compile(s, dates)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// at 返回时区 tz 中的钟点。
func at(t *testing.T, tz, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestOpenAtAndNextOpen 检查是否在工作时间内和下次上班的时间：时间段的边界、午休、跨时区、24:00、周末、特殊日期中的节假日和调休。
func TestOpenAtAndNextOpen(t *testing.T) {
	t.Parallel()
	const sh = "Asia/Shanghai"
	h := compile(t, sh, hours.Defaults.Week,
		hours.SpecialDate{Date: "2026-10-01", Name: "国庆节", Intervals: []hours.Interval{}},
		hours.SpecialDate{Date: "2026-10-10", Name: "调休", Intervals: []hours.Interval{{Start: "10:00", End: "16:00"}}},
	)
	split := compile(t, sh, hours.Week{Monday: []hours.Interval{{Start: "13:30", End: "24:00"}, {Start: "09:00", End: "12:00"}}})
	for _, c := range []struct {
		name string
		h    hours.Hours
		now  time.Time
		open bool
		next time.Time
	}{
		{"上班的时刻算上班", h, at(t, sh, "2026-09-28 09:00"), true, at(t, sh, "2026-09-28 09:00")},
		{"下班的时刻算下班，下次是第二天", h, at(t, sh, "2026-09-28 18:00"), false, at(t, sh, "2026-09-29 09:00")},
		{"上班之前", h, at(t, sh, "2026-09-28 08:59"), false, at(t, sh, "2026-09-28 09:00")},
		{"周五下班后到下周一", h, at(t, sh, "2026-09-25 20:00"), false, at(t, sh, "2026-09-28 09:00")},
		{"别的时区的时刻按工作时间的时区算", h, at(t, "UTC", "2026-09-28 02:00"), true, at(t, sh, "2026-09-28 10:00")},
		{"节假日不上班", h, at(t, sh, "2026-10-01 10:00"), false, at(t, sh, "2026-10-02 09:00")},
		{"调休的周六按特殊日期上班", h, at(t, sh, "2026-10-10 09:30"), false, at(t, sh, "2026-10-10 10:00")},
		{"调休的周六在时间段内", h, at(t, sh, "2026-10-10 15:59"), true, at(t, sh, "2026-10-10 15:59")},
		{"午休", split, at(t, sh, "2026-09-28 12:30"), false, at(t, sh, "2026-09-28 13:30")},
		{"到 24:00", split, at(t, sh, "2026-09-28 23:59"), true, at(t, sh, "2026-09-28 23:59")},
		{"只有周一上班，下次是下周一", split, at(t, sh, "2026-09-29 00:00"), false, at(t, sh, "2026-10-05 09:00")},
	} {
		if got := c.h.OpenAt(c.now); got != c.open {
			t.Errorf("%s：OpenAt = %v", c.name, got)
		}
		if got, ok := c.h.NextOpen(c.now); !ok || !got.Equal(c.next) {
			t.Errorf("%s：NextOpen = %v %v，期望 %v", c.name, got, ok, c.next)
		}
	}
}

// TestDaylightSaving 检查夏令时：按当地的钟点上班，切换前后的同一个钟点相差的不是 24 小时。
func TestDaylightSaving(t *testing.T) {
	t.Parallel()
	const ny = "America/New_York"
	nineToFive := []hours.Interval{{Start: "09:00", End: "17:00"}}
	h := compile(t, ny, hours.Week{Saturday: nineToFive, Sunday: nineToFive, Monday: nineToFive})
	// 2026-03-08 凌晨 2 点切换到夏令时
	if !h.OpenAt(at(t, ny, "2026-03-08 09:00")) || h.OpenAt(at(t, ny, "2026-03-08 08:59")) {
		t.Errorf("切换当天的钟点")
	}
	next, ok := h.NextOpen(at(t, ny, "2026-03-07 17:00"))
	if !ok || !next.Equal(at(t, ny, "2026-03-08 09:00")) || next.Sub(at(t, ny, "2026-03-07 09:00")) != 23*time.Hour {
		t.Errorf("跨过切换：%v", next)
	}
}

// TestNeverOpen 检查一周都不上班时没有下次上班的时间。
func TestNeverOpen(t *testing.T) {
	t.Parallel()
	h := compile(t, "UTC", hours.Week{})
	if _, ok := h.NextOpen(time.Now()); ok {
		t.Errorf("一周都不上班，却有下次上班的时间")
	}
}

// TestNormalize 检查校验：错误码和指向的字段，以及时间段按开始排列。
func TestNormalize(t *testing.T) {
	t.Parallel()
	iv := func(s, e string) hours.Interval { return hours.Interval{Start: s, End: e} }
	for _, c := range []struct {
		name  string
		s     hours.Schedule
		code  string
		field string
	}{
		{"不认识的时区", hours.Schedule{TimeZone: "Mars/Base"}, "hours.invalid_time_zone", "schedule.time_zone"},
		{"开始不早于结束", hours.Schedule{TimeZone: "UTC", Week: hours.Week{Tuesday: []hours.Interval{iv("09:00", "12:00"), iv("18:00", "18:00")}}}, "hours.invalid_interval", "schedule.week.tuesday[1].end"},
		{"重叠", hours.Schedule{TimeZone: "UTC", Week: hours.Week{Friday: []hours.Interval{iv("13:00", "18:00"), iv("09:00", "13:30")}}}, "hours.overlap", "schedule.week.friday[0].start"},
	} {
		err := c.s.Normalize("schedule.")
		if errorx.CodeOf(err) != c.code || errorx.FieldOf(err) != c.field {
			t.Errorf("%s：%v %q", c.name, err, errorx.FieldOf(err))
		}
	}
	s := hours.Schedule{TimeZone: "UTC", Week: hours.Week{Monday: []hours.Interval{iv("13:00", "18:00"), iv("09:00", "13:00")}}}
	if err := s.Normalize(""); err != nil || s.Week.Monday[0].Start != "09:00" || s.Week.Sunday == nil {
		t.Errorf("整理：%v %+v", err, s.Week)
	}
	_, err := hours.NormalizeSpecialDates("special_dates", []hours.SpecialDate{{Date: "2026-10-01"}, {Date: "2026-10-01"}})
	if errorx.CodeOf(err) != "hours.duplicate_date" || errorx.FieldOf(err) != "special_dates[1].date" {
		t.Errorf("重复的日期：%v", err)
	}
}
