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

// DeleteView 删除一条收件箱视图。
type DeleteView struct {
	db   *bun.DB
	down Downstream
}

// NewDeleteView 返回删除收件箱视图的业务动作。
func NewDeleteView(db *bun.DB, down Downstream) *DeleteView { return &DeleteView{db: db, down: down} }

// Handle 删除视图 id；共享视图需要 views:manage 权限并记录工作区操作日志。
func (a *DeleteView) Handle(ctx context.Context, sub identity.Subject, id string) error {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return err
	}
	g := NewViews(a.db)
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		v, err := g.WithDB(tx).Delete(ctx, sub, id)
		if err != nil {
			return err
		}
		if v.Scope == savedview.ScopeShared {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "view.deleted",
				TargetType: "view",
				TargetID:   v.ID,
				Properties: map[string]any{"name": v.Name},
			}); err != nil {
				return err
			}
		}
		return notifyViewsChanged(ctx, tx, a.down.Hub, a.down.Roles, sub.Tenant, v)
	})
}

// HTTP 处理 DELETE /t/{slug}/api/views/{id}。
func (a *DeleteView) HTTP(ctx context.Context, in *ViewIDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
