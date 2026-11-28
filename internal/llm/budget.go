package llm

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"
)

var (
	// ErrBudgetExceeded 是工作区模型调用费用或 token 用量已达到熔断上限。
	ErrBudgetExceeded = errorx.Define(errorx.ResourceExhausted, "llm.budget_exceeded")
	// ErrTenantNotFound 是管理端查询或配置预算覆写时指定的工作区不存在。
	ErrTenantNotFound = errorx.Define(errorx.NotFound, "llm.tenant_not_found")
)

// EffectiveBudget 返回工作区 tenantID 合并平台默认值与工作区覆写后的生效预算阈值。
func (c *Client) EffectiveBudget(ctx context.Context, tenantID string) (Budget, error) {
	s, err := c.settings.Get(ctx)
	if err != nil {
		return Budget{}, err
	}
	if tenantID == "" || c.tenantBudget == nil {
		return s.DefaultBudget, nil
	}
	ov, err := c.tenantBudget.Tenant(tenantID).Get(ctx)
	if err != nil {
		return Budget{}, err
	}
	return resolveBudget(s.DefaultBudget, ov), nil
}

// CheckBudget 在发起模型调用前校验工作区 tenantID 的当日与当月累计用量是否触达熔断阈值，超出时返回 ErrBudgetExceeded。
func (c *Client) CheckBudget(ctx context.Context, tenantID string) error {
	if tenantID == "" || c.db == nil {
		return nil
	}
	eff, err := c.EffectiveBudget(ctx, tenantID)
	if err != nil {
		return err
	}
	if eff.DailyCostUSD <= 0 && eff.MonthlyCostUSD <= 0 && eff.DailyTotalTokens <= 0 {
		return nil
	}
	nowUTC := time.Now().UTC()
	dayStart := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(nowUTC.Year(), nowUTC.Month(), 1, 0, 0, 0, 0, time.UTC)

	var usage struct {
		DayCost   float64 `bun:"day_cost"`
		MonthCost float64 `bun:"month_cost"`
		DayTokens int64   `bun:"day_tokens"`
	}
	err = c.db.NewRaw(`SELECT
			COALESCE(SUM(COALESCE(cost, 0)) FILTER (WHERE created_at >= ?), 0) AS day_cost,
			COALESCE(SUM(COALESCE(cost, 0)), 0) AS month_cost,
			COALESCE(SUM(prompt_tokens + completion_tokens) FILTER (WHERE created_at >= ?), 0) AS day_tokens
		FROM llm_calls
		WHERE tenant_id = ? AND created_at >= ?`,
		dayStart, dayStart, tenantID, monthStart).Scan(ctx, &usage)
	if err != nil {
		return errors.Wrap(err, "query tenant llm usage for budget check")
	}
	if eff.DailyCostUSD > 0 && usage.DayCost >= eff.DailyCostUSD {
		slog.WarnContext(ctx, "llm daily cost budget exceeded", "tenant_id", tenantID, "day_cost", usage.DayCost, "limit", eff.DailyCostUSD)
		return ErrBudgetExceeded.New()
	}
	if eff.MonthlyCostUSD > 0 && usage.MonthCost >= eff.MonthlyCostUSD {
		slog.WarnContext(ctx, "llm monthly cost budget exceeded", "tenant_id", tenantID, "month_cost", usage.MonthCost, "limit", eff.MonthlyCostUSD)
		return ErrBudgetExceeded.New()
	}
	if eff.DailyTotalTokens > 0 && usage.DayTokens >= eff.DailyTotalTokens {
		slog.WarnContext(ctx, "llm daily token budget exceeded", "tenant_id", tenantID, "day_tokens", usage.DayTokens, "limit", eff.DailyTotalTokens)
		return ErrBudgetExceeded.New()
	}
	return nil
}

// budgetTransport 在外层观测与上游 HTTP 请求之前按请求上下文归属的工作区检查用量预算。
type budgetTransport struct {
	client *Client
	next   http.RoundTripper
}

// RoundTrip 实现 http.RoundTripper，超限时直接返回 ErrBudgetExceeded 并阻断上游请求与调用记录。
func (t budgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if target, ok := req.Context().Value(targetCtxKey{}).(Target); ok && target.Tenant != "" {
		if err := t.client.CheckBudget(req.Context(), target.Tenant); err != nil {
			return nil, err
		}
	}
	return t.next.RoundTrip(req)
}

// TenantBudgetDTO 返回管理端查看单个工作区模型预算时的覆写配置与合并后生效阈值。
type TenantBudgetDTO struct {
	Override  TenantBudget `json:"override"`
	Effective Budget       `json:"effective"`
}

// TenantBudgetInput 是按工作区 ID 读取预算的路径参数。
type TenantBudgetInput struct {
	ID string `path:"id" format:"uuid"`
}

// SaveTenantBudgetInput 是保存工作区模型预算覆写的请求。
type SaveTenantBudgetInput struct {
	ID   string `path:"id" format:"uuid"`
	Body TenantBudget
}

// TenantBudgetOutput 是工作区模型预算接口的响应。
type TenantBudgetOutput struct {
	Body TenantBudgetDTO
}

// GetTenantBudget 读取指定工作区的模型预算覆写与生效阈值。
type GetTenantBudget struct {
	db     *bun.DB
	global *settings.Group[Settings]
	tenant *settings.Group[TenantBudget]
}

// NewGetTenantBudget 返回读取工作区模型预算的动作。
func NewGetTenantBudget(db *bun.DB, global *settings.Group[Settings], tenant *settings.Group[TenantBudget]) *GetTenantBudget {
	return &GetTenantBudget{db: db, global: global, tenant: tenant}
}

// Handle 校验工作区 tenantID 存在并返回其预算覆写配置与最终生效阈值。
func (a *GetTenantBudget) Handle(ctx context.Context, tenantID string) (*TenantBudgetDTO, error) {
	exists, err := a.db.NewSelect().Table("tenants").Where("id = ?", tenantID).Exists(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "check tenant for llm budget")
	}
	if !exists {
		return nil, ErrTenantNotFound.New()
	}
	g, err := a.global.Get(ctx)
	if err != nil {
		return nil, err
	}
	ov, err := a.tenant.Tenant(tenantID).Get(ctx)
	if err != nil {
		return nil, err
	}
	return &TenantBudgetDTO{Override: ov, Effective: resolveBudget(g.DefaultBudget, ov)}, nil
}

// HTTP 处理 GET /admin/api/tenants/{id}/llm-budget。
func (a *GetTenantBudget) HTTP(ctx context.Context, in *TenantBudgetInput) (*TenantBudgetOutput, error) {
	dto, err := a.Handle(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &TenantBudgetOutput{Body: *dto}, nil
}

// SaveTenantBudget 更新指定工作区的模型预算覆写并记录平台审计日志。
type SaveTenantBudget struct {
	db     *bun.DB
	global *settings.Group[Settings]
	tenant *settings.Group[TenantBudget]
}

// NewSaveTenantBudget 返回保存工作区模型预算覆写的动作。
func NewSaveTenantBudget(db *bun.DB, global *settings.Group[Settings], tenant *settings.Group[TenantBudget]) *SaveTenantBudget {
	return &SaveTenantBudget{db: db, global: global, tenant: tenant}
}

// Handle 校验工作区存在，在事务中写入工作区预算覆写并在有字段变更时记录审计日志。
func (a *SaveTenantBudget) Handle(ctx context.Context, sub identity.Subject, tenantID string, updated TenantBudget) (*TenantBudgetDTO, error) {
	var name string
	if err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var row struct {
			Name string `bun:"name"`
		}
		err := tx.NewSelect().Table("tenants").Column("name").Where("id = ?", tenantID).Scan(ctx, &row)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrTenantNotFound.New()
			}
			return errors.Wrap(err, "load tenant for llm budget")
		}
		name = row.Name
		var changed []string
		err = a.tenant.WithDB(tx).Tenant(tenantID).Update(ctx, func(cur *TenantBudget) error {
			changed = formatBudgetDiff(*cur, updated)
			*cur = updated
			return nil
		})
		if err != nil || len(changed) == 0 {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event:      "llm_budget.changed",
			TargetType: "tenant",
			TargetID:   tenantID,
			Properties: map[string]any{"name": name, "changed": changed},
		})
	}); err != nil {
		return nil, err
	}
	return NewGetTenantBudget(a.db, a.global, a.tenant).Handle(ctx, tenantID)
}

// HTTP 处理 PUT /admin/api/tenants/{id}/llm-budget。
func (a *SaveTenantBudget) HTTP(ctx context.Context, in *SaveTenantBudgetInput) (*TenantBudgetOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &TenantBudgetOutput{Body: *dto}, nil
}
