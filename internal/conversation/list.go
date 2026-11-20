package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/auth"
)

// ListInput 是会话列表的查询条件。收件箱的各个列表、数量和排序在收件箱的计划项中加入。
type ListInput struct {
	page.OffsetParams
	Status Status `query:"status" doc:"只列出这个状态的会话；为空时列出全部"`
}

// ListOutput 是一页会话。
type ListOutput struct {
	Body page.Offset[ConversationDTO]
}

// List 列出当前工作区的会话，最近有消息的在前。
type List struct {
	db *bun.DB
}

// NewList 返回会话列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的会话，可以按状态筛选。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[ConversationDTO], error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	var rows []Conversation
	q := a.db.NewSelect().Model(&rows).Where("tenant_id = ?", sub.Tenant).
		OrderExpr("last_message_at DESC, id DESC").Limit(in.Limit()).Offset(in.Offset())
	if in.Status != "" {
		q = q.Where("status = ?", in.Status)
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list conversations")
	}
	items, err := toDTOs(ctx, a.db, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/conversations。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
