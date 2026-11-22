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

// CreateViewInput 是新建收件箱视图的 HTTP 请求。
type CreateViewInput struct {
	Body ViewCreateParams
}

// CreateView 在工作区中新建一条共享或个人收件箱视图。
type CreateView struct {
	db   *bun.DB
	down Downstream
}

// NewCreateView 返回新建收件箱视图的业务动作。
func NewCreateView(db *bun.DB, down Downstream) *CreateView { return &CreateView{db: db, down: down} }

// Handle 校验配置引用后创建视图；共享视图需要 views:manage 权限并记录工作区操作日志，提交后实时通知成员刷新视图列表。
func (a *CreateView) Handle(ctx context.Context, sub identity.Subject, p ViewCreateParams) (*ViewDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	g := NewViews(a.db)
	var created savedview.View[ViewConfig]
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		cfg, err := validateViewConfig(ctx, tx, sub.Tenant, p.Config)
		if err != nil {
			return err
		}
		v, err := g.WithDB(tx).Create(ctx, sub, savedview.CreateInput[ViewConfig]{
			Scope:  p.Scope,
			Name:   p.Name,
			Config: cfg,
		})
		if err != nil {
			return err
		}
		created = v
		if v.Scope == savedview.ScopeShared {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "view.created",
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
	dto := viewToDTO(g, sub, created)
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/views。
func (a *CreateView) HTTP(ctx context.Context, in *CreateViewInput) (*ViewOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ViewOutput{Body: *dto}, nil
}
