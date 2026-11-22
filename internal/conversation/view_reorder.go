package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/savedview"

	"agents-desk/internal/auth"
)

// ReorderViewsParams 是重排收件箱视图顺序的参数。
type ReorderViewsParams struct {
	Scope savedview.Scope `json:"scope" doc:"要重排的作用域：shared 或 personal"`
	IDs   []string        `json:"ids" minItems:"1" maxItems:"100" doc:"按新顺序排列的视图 ID 列表"`
}

// ReorderViewsInput 是重排视图顺序的 HTTP 请求。
type ReorderViewsInput struct {
	Body ReorderViewsParams
}

// ReorderViews 按指定顺序重排某作用域下的收件箱视图。
type ReorderViews struct {
	db   *bun.DB
	down Downstream
}

// NewReorderViews 返回重排收件箱视图的业务动作。
func NewReorderViews(db *bun.DB, down Downstream) *ReorderViews {
	return &ReorderViews{db: db, down: down}
}

// Handle 调整 scope 下视图的展示顺序并推送实时更新。
func (a *ReorderViews) Handle(ctx context.Context, sub identity.Subject, p ReorderViewsParams) error {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return err
	}
	g := NewViews(a.db)
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := g.WithDB(tx).Reorder(ctx, sub, p.Scope, p.IDs); err != nil {
			return err
		}
		v := savedview.View[ViewConfig]{Scope: p.Scope}
		if p.Scope == savedview.ScopePersonal {
			v.Owner = sub.Ref()
		}
		return notifyViewsChanged(ctx, tx, a.down.Hub, a.down.Roles, sub.Tenant, v)
	})
}

// HTTP 处理 PUT /t/{slug}/api/views/reorder。
func (a *ReorderViews) HTTP(ctx context.Context, in *ReorderViewsInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.Body)
}
