// Package platformoverview 提供管理员端首页概览接口，按当前管理员的权限汇总工作区、成员、会话、模型调用、队列状态与最近操作。
package platformoverview

import (
	"context"
	"log/slog"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/audit"
	"agents-desk/internal/auth"
	"agents-desk/internal/tenant"
)

// Input 是概览查询参数。
type Input struct {
	TZ string `query:"tz" doc:"统计“今日”与“近 7 天”边界使用的 IANA 时区，默认 UTC"`
}

// TenantSummary 是平台工作区统计摘要。
type TenantSummary struct {
	Total        int `bun:"total" json:"total"`
	Active       int `bun:"active" json:"active"`
	Disabled     int `bun:"disabled" json:"disabled"`
	NewLast7Days int `bun:"new_last_7_days" json:"new_last_7_days"`
}

// AdminSummary 是平台管理员账号统计摘要。
type AdminSummary struct {
	Total    int `bun:"total" json:"total"`
	Active   int `bun:"active" json:"active"`
	Disabled int `bun:"disabled" json:"disabled"`
}

// UsageSummary 是跨工作区的平台业务活跃度与模型调用汇总。
type UsageSummary struct {
	MembersTotal           int     `bun:"members_total" json:"members_total"`
	ContactsTotal          int     `bun:"contacts_total" json:"contacts_total"`
	ConversationsTotal     int     `bun:"conversations_total" json:"conversations_total"`
	ConversationsOpen      int     `bun:"conversations_open" json:"conversations_open"`
	ConversationsToday     int     `bun:"conversations_today" json:"conversations_today"`
	ConversationsLast7Days int     `bun:"conversations_last_7_days" json:"conversations_last_7_days"`
	LLMCallsToday          int     `bun:"llm_calls_today" json:"llm_calls_today"`
	LLMCallsLast7Days      int     `bun:"llm_calls_last_7_days" json:"llm_calls_last_7_days"`
	LLMFailuresLast7Days   int     `bun:"llm_failures_last_7_days" json:"llm_failures_last_7_days"`
	LLMCostLast7Days       float64 `bun:"llm_cost_last_7_days" json:"llm_cost_last_7_days"`
}

// QueueSummary 是后台异步队列待转发与失败任务汇总。
type QueueSummary struct {
	OutboxPending int `json:"outbox_pending"`
	FailedTotal   int `json:"failed_total"`
}

// RecentTenant 是最近创建的工作区摘要项。
type RecentTenant struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Slug      string        `json:"slug"`
	URL       string        `json:"url"`
	LogoURL   string        `json:"logo_url"`
	Status    tenant.Status `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
}

// Overview 是管理后台概览接口响应体，各区块按当前管理员的权限按需填充。
type Overview struct {
	Tenants       *TenantSummary `json:"tenants,omitempty"`
	Admins        *AdminSummary  `json:"admins,omitempty"`
	Usage         *UsageSummary  `json:"usage,omitempty"`
	Queue         *QueueSummary  `json:"queue,omitempty"`
	RecentTenants []RecentTenant `json:"recent_tenants,omitempty"`
	RecentAudits  []audit.Item   `json:"recent_audits,omitempty"`
}

// Output 是 GET /overview 的 HTTP 响应。
type Output struct {
	Body Overview
}

// Get 读取平台概览数据。
type Get struct {
	db    *bun.DB
	files *filesystem.Service
	addr  tenant.Addresses
}

// NewGet 返回读取平台概览数据的动作。
func NewGet(db *bun.DB, files *filesystem.Service, addr tenant.Addresses) *Get {
	return &Get{db: db, files: files, addr: addr}
}

// Handle 按当前管理员 sub 的权限查询平台各维度汇总指标。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, in Input) (Overview, error) {
	loc := time.UTC
	if in.TZ != "" {
		loaded, err := time.LoadLocation(in.TZ)
		if err != nil {
			slog.WarnContext(ctx, "invalid overview timezone, fallback to UTC", "tz", in.TZ, "error", err)
		} else {
			loc = loaded
		}
	}
	now := time.Now().In(loc)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC()
	last7Start := todayStart.AddDate(0, 0, -6)

	var out Overview

	if sub.Can(string(auth.TenantsRead)) {
		var ts TenantSummary
		if err := a.db.NewRaw(`
SELECT
    count(*) AS total,
    count(*) FILTER (WHERE disabled_at IS NULL) AS active,
    count(*) FILTER (WHERE disabled_at IS NOT NULL) AS disabled,
    count(*) FILTER (WHERE created_at >= ?) AS new_last_7_days
FROM tenants`, last7Start).Scan(ctx, &ts); err != nil {
			return Overview{}, errors.Wrap(err, "query tenant summary")
		}
		out.Tenants = &ts

		var usage UsageSummary
		if err := a.db.NewRaw(`SELECT count(*) FROM members`).Scan(ctx, &usage.MembersTotal); err != nil {
			return Overview{}, errors.Wrap(err, "count members")
		}
		if err := a.db.NewRaw(`SELECT count(*) FROM contacts`).Scan(ctx, &usage.ContactsTotal); err != nil {
			return Overview{}, errors.Wrap(err, "count contacts")
		}
		if err := a.db.NewRaw(`
SELECT
    count(*) AS conversations_total,
    count(*) FILTER (WHERE status = 'open') AS conversations_open,
    count(*) FILTER (WHERE created_at >= ?) AS conversations_today,
    count(*) FILTER (WHERE created_at >= ?) AS conversations_last_7_days
FROM conversations`, todayStart, last7Start).Scan(ctx, &usage); err != nil {
			return Overview{}, errors.Wrap(err, "query conversation summary")
		}
		if err := a.db.NewRaw(`
SELECT
    count(*) FILTER (WHERE created_at >= ?) AS llm_calls_today,
    count(*) FILTER (WHERE created_at >= ?) AS llm_calls_last_7_days,
    count(*) FILTER (WHERE created_at >= ? AND error <> '') AS llm_failures_last_7_days,
    coalesce(sum(cost) FILTER (WHERE created_at >= ?), 0)::double precision AS llm_cost_last_7_days
FROM llm_calls`, todayStart, last7Start, last7Start, last7Start).Scan(ctx, &usage); err != nil {
			return Overview{}, errors.Wrap(err, "query llm summary")
		}
		out.Usage = &usage

		var rows []tenant.Tenant
		if err := a.db.NewSelect().Model(&rows).Order("created_at DESC", "id DESC").Limit(5).Scan(ctx); err != nil {
			return Overview{}, errors.Wrap(err, "list recent tenants")
		}
		var fileIDs []uuid.UUID
		for _, r := range rows {
			if r.LogoFileID != nil {
				fileIDs = append(fileIDs, *r.LogoFileID)
			}
		}
		var logos map[uuid.UUID]filesystem.File
		if len(fileIDs) > 0 {
			var err error
			logos, err = a.files.GetMany(ctx, fileIDs)
			if err != nil {
				return Overview{}, err
			}
		}
		recent := make([]RecentTenant, 0, len(rows))
		for _, r := range rows {
			var logoURL string
			if r.LogoFileID != nil {
				logoURL = logos[*r.LogoFileID].URL
			}
			recent = append(recent, RecentTenant{
				ID: r.ID, Name: r.Name, Slug: r.Slug, URL: a.addr.URL(&r),
				LogoURL: logoURL, Status: r.Status(), CreatedAt: r.CreatedAt,
			})
		}
		out.RecentTenants = recent
	}

	if sub.Can(string(auth.PlatformAdminsManage)) {
		var as AdminSummary
		if err := a.db.NewRaw(`
SELECT
    count(*) AS total,
    count(*) FILTER (WHERE disabled_at IS NULL) AS active,
    count(*) FILTER (WHERE disabled_at IS NOT NULL) AS disabled
FROM platform_admins`).Scan(ctx, &as); err != nil {
			return Overview{}, errors.Wrap(err, "query admin summary")
		}
		out.Admins = &as
	}

	if sub.Can(string(auth.QueueManage)) {
		var qs QueueSummary
		if err := a.db.NewRaw(`SELECT count(*) FROM queue_outbox`).Scan(ctx, &qs.OutboxPending); err != nil {
			return Overview{}, errors.Wrap(err, "count queue outbox")
		}
		if err := a.db.NewRaw(`SELECT count(*) FROM queue_failed_jobs`).Scan(ctx, &qs.FailedTotal); err != nil {
			return Overview{}, errors.Wrap(err, "count failed jobs")
		}
		out.Queue = &qs
	}

	if sub.Can(string(auth.AuditRead)) {
		logs, err := audit.NewList(a.db).Handle(ctx, audit.ListInput{
			OffsetParams: page.OffsetParams{Page: 1, PerPage: 5},
		})
		if err != nil {
			return Overview{}, err
		}
		out.RecentAudits = logs.Items
	}

	return out, nil
}

// HTTP 处理 GET /overview。
func (a *Get) HTTP(ctx context.Context, in *Input) (*Output, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &Output{Body: res}, nil
}
