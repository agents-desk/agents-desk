package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/savedview"

	"agents-desk/internal/auth"
)

// UpdateViewInput 是修改收件箱视图的 HTTP 请求。
type UpdateViewInput struct {
	ID   string `path:"id" format:"uuid"`
	Body ViewUpdateParams
}

// UpdateView 修改已保存收件箱视图的名称和筛选配置。
type UpdateView struct {
	db   *bun.DB
	down Downstream
}

// NewUpdateView 返回修改收件箱视图的业务动作。
func NewUpdateView(db *bun.DB, down Downstream) *UpdateView { return &UpdateView{db: db, down: down} }

// Handle 修改视图 id 的名称与配置；共享视图需要 views:manage 权限并记录工作区操作日志。
func (a *UpdateView) Handle(ctx context.Context, sub identity.Subject, id string, p ViewUpdateParams) (*ViewDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	g := NewViews(a.db)
	var updated savedview.View[ViewConfig]
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		cfg, err := validateViewConfig(ctx, tx, sub.Tenant, p.Config)
		if err != nil {
			return err
		}
		v, err := g.WithDB(tx).Update(ctx, sub, id, savedview.UpdateInput[ViewConfig]{
			Name:   p.Name,
			Config: cfg,
		})
		if err != nil {
			return err
		}
		updated = v
		if v.Scope == savedview.ScopeShared {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "view.updated",
				TargetType: "view",
				TargetID:   v.ID,
				Properties: map[string]any{"name": v.Name},
			}); err != nil {
				return err
			}
		}
		return notifyViewsChanged(ctx, tx, a.down.Hub, a.down.Roles, sub.Tenant, v)
	})
	if err != nil {
		return nil, err
	}
	dto := viewToDTO(g, sub, updated)
	return &dto, nil
}

// HTTP 处理 PUT /t/{slug}/api/views/{id}。
func (a *UpdateView) HTTP(ctx context.Context, in *UpdateViewInput) (*ViewOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ViewOutput{Body: *dto}, nil
}
