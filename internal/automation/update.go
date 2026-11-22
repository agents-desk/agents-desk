package automation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// UpdateBody 是修改自动化规则的请求体：第一版只修改启用状态。
type UpdateBody struct {
	Enabled bool `json:"enabled" doc:"是否启用这条自动化规则"`
}

// UpdateInput 是修改自动化规则的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateBody
}

// RuleOutput 是返回一条自动化规则的响应。
type RuleOutput struct {
	Body RuleDTO
}

// Update 启用或停用工作区的一条自动化规则，并记录操作日志。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改自动化规则的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 把 sub 所在工作区中的规则 id 的启用状态改为 enabled。规则不存在或属于别的工作区时返回 ErrNotFound；
// 没有 tenant-settings:manage 权限时返回 ErrForbidden。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, enabled bool) (*RuleDTO, error) {
	if sub.Type != identity.System && !sub.Can(string(auth.TenantSettingsManage)) {
		return nil, ErrForbidden.New()
	}
	var r Rule
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		err := tx.NewSelect().Model(&r).Where("id = ? AND tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "load automation rule")
		}
		if r.Enabled == enabled {
			return nil
		}
		r.Enabled = enabled
		if _, err := tx.NewUpdate().Model(&r).Column("enabled", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save automation rule")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "automation.updated", TargetType: "automation_rule", TargetID: r.ID,
			Properties: map[string]any{"builtin_key": string(r.BuiltinKey), "enabled": enabled},
		})
	})
	if err != nil {
		return nil, err
	}
	dto := toDTO(r)
	return &dto, nil
}

// HTTP 处理 PUT /t/{slug}/api/automations/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*RuleOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.Enabled)
	if err != nil {
		return nil, err
	}
	return &RuleOutput{Body: *dto}, nil
}
