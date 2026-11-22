package attribute

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// Delete 删除当前工作区中的一个自定义属性定义。历史会话和联系人上已保存的旧值保留在 JSONB 中，便于时间线追溯。
type Delete struct {
	db *bun.DB
}

// NewDelete 返回删除属性定义的动作。
func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

// Handle 删除 sub 所在工作区中的属性定义 id 并记录操作日志。不存在时返回 ErrNotFound。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		d, err := Find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model(d).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "delete attribute definition")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "attribute.deleted", TargetType: "attribute", TargetID: id,
			Properties: map[string]any{"target": string(d.Target), "key": d.Key, "name": d.Name},
		})
	})
}

// HTTP 处理 DELETE /t/{slug}/api/attributes/{id}。
func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
