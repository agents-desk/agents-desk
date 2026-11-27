package channel

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// ListItem 是工作区渠道列表中的一项轻量概要（不含敏感配置与密钥），供收件箱筛选、视图、报表与商务沟通筛选使用。
type ListItem struct {
	ID            string   `json:"id"`
	Kind          Kind     `json:"kind"`
	Audience      Audience `json:"audience" doc:"渠道服务对象域：customer（外部客户）、partner（商务）或 internal（内部员工）"`
	Name          string   `json:"name"`
	TeamID        *string  `json:"team_id,omitempty" doc:"默认进入的团队收件箱 ID"`
	OwnerMemberID *string  `json:"owner_member_id,omitempty" doc:"个人商务渠道所属的成员 ID；公共渠道为空"`
	Enabled       bool     `json:"enabled" doc:"渠道是否启用"`
}

// ListOutput 是工作区渠道列表的响应。
type ListOutput struct {
	Body []ListItem
}

// List 读取当前工作区的全部渠道概要（含已停用渠道，历史会话和报表需要按渠道筛选），按创建时间顺序排列。
type List struct {
	db *bun.DB
}

// NewList 返回读取工作区渠道列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的全部渠道概要。
func (a *List) Handle(ctx context.Context, sub identity.Subject) ([]ListItem, error) {
	var rows []Channel
	err := a.db.NewSelect().Model(&rows).
		Column("id", "kind", "audience", "name", "team_id", "owner_member_id", "enabled").
		Where("tenant_id = ?", sub.Tenant).
		OrderExpr("created_at ASC, id ASC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list channels")
	}
	out := make([]ListItem, len(rows))
	for i, c := range rows {
		out[i] = ListItem{
			ID: c.ID, Kind: c.Kind, Audience: c.EffectiveAudience(),
			Name: c.Name, TeamID: c.TeamID, OwnerMemberID: c.OwnerMemberID, Enabled: c.Enabled,
		}
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/channels。
func (a *List) HTTP(ctx context.Context, _ *struct{}) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: items}, nil
}
