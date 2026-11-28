// Package report 是工作区的报表模块：概览、客服（会话/响应时间/满意度）、工单、商务沟通、成员人效和 AI 与翻译。
// 所有统计均按工作区（sub.Tenant）严格隔离，时间边界由 go-tenon/tenon/period 在 Go 侧按 IANA 时区换算为 UTC 半开区间传入 SQL，不使用数据库时钟函数。
package report

import (
	"context"
	"math"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/channel"
	"agents-desk/internal/hours"
	"agents-desk/internal/team"
)

// PeriodMeta 是报表响应中携带的时间窗口元信息；独立命名以避免与 filesystem.Meta 在 OpenAPI schema 中重名。
type PeriodMeta period.Meta

// FilterParams 是支持按服务对象、团队收件箱与渠道筛选的报表通用输入参数。
type FilterParams struct {
	period.Params
	Audience  channel.Audience `query:"audience" doc:"按服务对象筛选（customer、partner 或 internal）"`
	TeamID    string           `query:"team_id" format:"uuid" doc:"按团队收件箱筛选（含已停用团队）"`
	ChannelID string           `query:"channel_id" format:"uuid" doc:"按渠道筛选（含已停用渠道）"`
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
		Now:         database.Now(),
		DefaultZone: defZone,
		DefaultDays: 30,
		MaxDays:     366,
	})
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
