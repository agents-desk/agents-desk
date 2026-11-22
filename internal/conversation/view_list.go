package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/savedview"

	"agents-desk/internal/auth"
)

// ListViewsInput 是列出收件箱视图的请求参数。
type ListViewsInput struct {
	Scope savedview.Scope `query:"scope" doc:"可选：按作用域筛选（shared 或 personal）"`
}

// ListViewsOutput 是视图列表响应。
type ListViewsOutput struct {
	Body []ViewDTO
}

// ListViews 列出当前成员在工作区中可见的全部收件箱视图。
type ListViews struct {
	db *bun.DB
}

// NewListViews 返回列出收件箱视图的业务动作。
func NewListViews(db *bun.DB) *ListViews { return &ListViews{db: db} }

// Handle 返回 sub 可见的共享和个人视图，共享在前、个人在后。
func (a *ListViews) Handle(ctx context.Context, sub identity.Subject, scope savedview.Scope) ([]ViewDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	g := NewViews(a.db)
	items, err := g.List(ctx, sub, scope)
	if err != nil {
		return nil, err
	}
	out := make([]ViewDTO, len(items))
	for i, v := range items {
		out[i] = viewToDTO(g, sub, v)
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/views。
func (a *ListViews) HTTP(ctx context.Context, in *ListViewsInput) (*ListViewsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub, in.Scope)
	if err != nil {
		return nil, err
	}
	return &ListViewsOutput{Body: items}, nil
}
