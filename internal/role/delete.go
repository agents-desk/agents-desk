package role

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// Delete 删除当前工作区的一个角色。还有成员使用它时不能删除：每位成员都要有一个角色。
type Delete struct {
	db *bun.DB
}

// NewDelete 返回删除角色的动作。
func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

// Handle 删除 sub 所在工作区的角色 id 并记录操作日志。角色不存在、或者不是这个工作区自己的角色时返回 ErrNotFound；
// 还有成员使用它时返回 ErrInUse，带有人数 count。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		r, err := Lock(ctx, tx, sub.Tenant, id)
		if err != nil {
			return err
		}
		roles := authz.NewStore(tx, auth.Catalog)
		counts, err := memberCounts(ctx, roles, sub.Tenant)
		if err != nil {
			return err
		}
		if n := counts[id]; n > 0 {
			return ErrInUse.New(errorx.Public("count", n))
		}
		if err := roles.DeleteRole(ctx, id, sub.Tenant); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "role.deleted", TargetType: "role", TargetID: id, Properties: map[string]any{"name": r.Name},
		})
	})
}

// HTTP 处理 DELETE /t/{slug}/api/roles/{id}。
func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
