package tag

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// Delete 删除当前工作区中的一个标签，已打在会话和联系人上的关联随之移除。
type Delete struct {
	db *bun.DB
}

// NewDelete 返回删除标签的动作。
func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

// Handle 删除 sub 所在工作区中的标签 id 并记录操作日志。不存在时返回 ErrNotFound。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := Find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model(t).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "delete tag")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "tag.deleted", TargetType: "tag", TargetID: id, Properties: map[string]any{"name": t.Name},
		})
	})
}

// HTTP 处理 DELETE /t/{slug}/api/tags/{id}。
func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
