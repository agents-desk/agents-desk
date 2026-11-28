package telegram

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/channel"
)

// ListInput 是 Telegram 机器人列表的查询条件。
type ListInput struct {
	page.OffsetParams
	Audience channel.Audience `query:"audience" doc:"可选：按服务对象筛选（customer、partner 或 internal）"`
}

// ListOutput 是一页 Telegram 机器人渠道。
type ListOutput struct {
	Body page.Offset[TelegramDTO]
}

// List 列出当前工作区的 Telegram 机器人渠道，先建的在前。
type List struct {
	db *bun.DB
}

// NewList 返回 Telegram 渠道列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的全部 Telegram 渠道（含停用），可按服务对象筛选。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[TelegramDTO], error) {
	if in == nil {
		in = &ListInput{}
	}
	var rows []channel.Channel
	q := a.db.NewSelect().Model(&rows).Where("tenant_id = ? AND kind = ? AND archived_at IS NULL", sub.Tenant, channel.Telegram)
	if in.Audience != "" {
		q = q.Where("audience = ?", in.Audience)
	}
	total, err := q.OrderExpr("created_at, id").Limit(in.Limit()).Offset(in.Offset()).ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list telegram channels")
	}
	items, err := toDTOs(ctx, a.db, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/telegrams。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
