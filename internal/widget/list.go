package widget

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/channel"
)

// ListInput 是聊天插件列表的查询条件。
type ListInput struct {
	page.OffsetParams
}

// ListOutput 是一页聊天插件。
type ListOutput struct {
	Body page.Offset[WidgetDTO]
}

// List 列出当前工作区的聊天插件，先建的在前。
type List struct {
	db *bun.DB
}

// NewList 返回聊天插件列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的聊天插件，包括停用的。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[WidgetDTO], error) {
	var rows []channel.Channel
	total, err := a.db.NewSelect().Model(&rows).Where("tenant_id = ? AND kind = ?", sub.Tenant, channel.Widget).
		OrderExpr("created_at, id").Limit(in.Limit()).Offset(in.Offset()).ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list widgets")
	}
	items, err := toDTOs(ctx, a.db, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/widgets。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
