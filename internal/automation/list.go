package automation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// ListOutput 是工作区自动化规则列表的响应。
type ListOutput struct {
	Body []RuleDTO
}

// List 列出工作区的自动化规则，确保内置规则存在并按顺序返回。
type List struct {
	db *bun.DB
}

// NewList 返回列出自动化规则的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的全部自动化规则，按执行顺序排列。需要 tenant-settings:manage 权限。
func (a *List) Handle(ctx context.Context, sub identity.Subject) ([]RuleDTO, error) {
	if sub.Type != identity.System && !sub.Can(string(auth.TenantSettingsManage)) {
		return nil, ErrForbidden.New()
	}
	if err := EnsureBuiltins(ctx, a.db, sub.Tenant); err != nil {
		return nil, err
	}
	var rules []Rule
	err := a.db.NewSelect().Model(&rules).Where("tenant_id = ?", sub.Tenant).
		OrderExpr("trigger ASC, position ASC, id ASC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list automation rules")
	}
	out := make([]RuleDTO, len(rules))
	for i, r := range rules {
		out[i] = toDTO(r)
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/automations。
func (a *List) HTTP(ctx context.Context, _ *struct{}) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	list, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: list}, nil
}
