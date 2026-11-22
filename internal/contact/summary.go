package contact

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// SummaryDTO 是联系人模块的概览：当前成员可见的各类联系人数量，以及可见的渠道列表（用于联系人列表按渠道筛选）。
type SummaryDTO struct {
	All      int64             `json:"all" doc:"全部联系人数量"`
	Users    int64             `json:"users" doc:"用户数量"`
	Leads    int64             `json:"leads" doc:"线索数量"`
	Channels []channel.Summary `json:"channels" doc:"当前成员可见的渠道，供联系人列表按渠道筛选"`
}

// SummaryOutput 是联系人概览的响应。
type SummaryOutput struct {
	Body SummaryDTO
}

// GetSummary 读取工作区联系人的各类型数量和渠道列表。
type GetSummary struct {
	db *bun.DB
}

// NewGetSummary 返回读取联系人概览的动作。
func NewGetSummary(db *bun.DB) *GetSummary { return &GetSummary{db: db} }

// Handle 返回 sub 所在工作区中 sub 能看到的全部/用户/线索数量，以及可见渠道概要列表。
func (a *GetSummary) Handle(ctx context.Context, sub identity.Subject) (*SummaryDTO, error) {
	var counts []struct {
		Kind  Kind  `bun:"kind"`
		Count int64 `bun:"count"`
	}
	err := visible(
		a.db.NewSelect().TableExpr("contacts AS contact").
			Column("contact.kind").ColumnExpr("count(*) AS count").
			Where("contact.tenant_id = ?", sub.Tenant),
		sub,
	).Group("contact.kind").Scan(ctx, &counts)
	if err != nil {
		return nil, errors.Wrap(err, "count contacts by kind")
	}
	out := SummaryDTO{Channels: []channel.Summary{}}
	for _, r := range counts {
		out.All += r.Count
		switch r.Kind {
		case User:
			out.Users = r.Count
		case Lead:
			out.Leads = r.Count
		}
	}
	var chRows []channel.Channel
	if err := visibleChannels(
		a.db.NewSelect().Model(&chRows).Where("channel.tenant_id = ?", sub.Tenant),
		sub,
	).OrderExpr("channel.name ASC, channel.id ASC").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list channels for contacts")
	}
	for i := range chRows {
		out.Channels = append(out.Channels, chRows[i].Summary())
	}
	return &out, nil
}

// HTTP 处理 GET /t/{slug}/api/contacts/summary。
func (a *GetSummary) HTTP(ctx context.Context, _ *struct{}) (*SummaryOutput, error) {
	sub, _ := identity.FromContext(ctx)
	s, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &SummaryOutput{Body: *s}, nil
}
