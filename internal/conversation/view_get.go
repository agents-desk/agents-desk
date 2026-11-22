package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// GetView 读取单条可见的收件箱视图。
type GetView struct {
	db *bun.DB
}

// NewGetView 返回读取单条收件箱视图的业务动作。
func NewGetView(db *bun.DB) *GetView { return &GetView{db: db} }

// Handle 读取 sub 可见的视图 id；跨工作区或属于他人个人视图时返回 savedview.ErrNotFound。
func (a *GetView) Handle(ctx context.Context, sub identity.Subject, id string) (*ViewDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	g := NewViews(a.db)
	v, err := g.Get(ctx, sub, id)
	if err != nil {
		return nil, err
	}
	dto := viewToDTO(g, sub, v)
	return &dto, nil
}

// HTTP 处理 GET /t/{slug}/api/views/{id}。
func (a *GetView) HTTP(ctx context.Context, in *ViewIDInput) (*ViewOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ViewOutput{Body: *dto}, nil
}
