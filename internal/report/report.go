// Package report 是工作区的基础报表：会话、响应时间、满意度、成员和翻译。
// 所有统计均按工作区（sub.Tenant）严格隔离，仅统计服务型会话（kind = 'service'），
// 时间边界由 go-tenon/tenon/period 在 Go 侧按 IANA 时区换算为 UTC 半开区间传入 SQL，不使用数据库时钟函数。
package report

import (
	"context"
	"math"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/channel"
	"agents-desk/internal/hours"
	"agents-desk/internal/team"
)

// now 返回当前 UTC 时刻；独立为函数便于在 synctest 下与假时钟协同。
var now = func() time.Time { return time.Now().UTC() }

// PeriodMeta 是报表响应中携带的时间窗口元信息；独立命名以避免与 filesystem.Meta 在 OpenAPI schema 中重名。
type PeriodMeta period.Meta

// FilterParams 是支持按团队收件箱与渠道筛选的报表通用输入参数。
type FilterParams struct {
	period.Params
	TeamID    string `query:"team_id" format:"uuid" doc:"按团队收件箱筛选（含已停用团队）"`
	ChannelID string `query:"channel_id" format:"uuid" doc:"按渠道筛选（含已停用渠道）"`
}

// LanguageCount 是按语言聚合的计数项。
type LanguageCount struct {
	Language string `json:"language" doc:"BCP 47 语言标签，未知或未确定时为 und 或空"`
	Count    int64  `json:"count"`
}

// resolveWindow 读取工作区默认时区并解析时间窗口参数；同时校验可选的团队与渠道筛选是否属于当前工作区。
func resolveWindow(ctx context.Context, db bun.IDB, sub identity.Subject, f FilterParams) (period.Window, error) {
	if f.TeamID != "" {
		ok, err := db.NewSelect().Table("teams").Where("id = ? AND tenant_id = ?", f.TeamID, sub.Tenant).Exists(ctx)
		if err != nil {
			return period.Window{}, errors.Wrap(err, "check report team")
		}
		if !ok {
			return period.Window{}, team.ErrNotFound.New()
		}
	}
	if f.ChannelID != "" {
		ok, err := db.NewSelect().Table("channels").Where("id = ? AND tenant_id = ?", f.ChannelID, sub.Tenant).Exists(ctx)
		if err != nil {
			return period.Window{}, errors.Wrap(err, "check report channel")
		}
		if !ok {
			return period.Window{}, channel.ErrNotFound.New()
		}
	}
	return resolvePeriodOnly(ctx, db, sub.Tenant, f.Params)
}

// resolvePeriodOnly 读取工作区默认时区并解析时间窗口参数（用于无团队/渠道维度的翻译报表）。
func resolvePeriodOnly(ctx context.Context, db bun.IDB, tenantID string, p period.Params) (period.Window, error) {
	s, err := hours.NewGroup(db).Tenant(tenantID).Get(ctx)
	if err != nil {
		return period.Window{}, errors.Wrap(err, "load business hours time zone")
	}
	defZone := s.TimeZone
	if defZone == "" {
		defZone = hours.Defaults.TimeZone
	}
	return period.Resolve(p, period.Options{
		Now:         now(),
		DefaultZone: defZone,
		DefaultDays: 30,
		MaxDays:     366,
	})
}

// bucketExpr 返回按粒度 g 在参数化时区 ? 下把时间列 col 截断为桶标签字符串的 PostgreSQL 表达式，
// 格式与 period.Window.Buckets() 保持一致。
func bucketExpr(col string, g period.Granularity) string {
	switch g {
	case period.Hour:
		return "to_char(date_trunc('hour', " + col + " AT TIME ZONE ?), 'YYYY-MM-DD HH24:MI')"
	case period.Week:
		return "to_char(date_trunc('week', " + col + " AT TIME ZONE ?), 'YYYY-MM-DD')"
	case period.Month:
		return "to_char(date_trunc('month', " + col + " AT TIME ZONE ?), 'YYYY-MM-DD')"
	default:
		return "to_char(date_trunc('day', " + col + " AT TIME ZONE ?), 'YYYY-MM-DD')"
	}
}

// round1 把浮点数保留 1 位小数。
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// roundPtr 把指针指向的浮点数保留 1 位小数，nil 原样返回。
func roundPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := round1(*v)
	return &r
}
