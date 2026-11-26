package tickettype

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// Delete 删除当前工作区中的一个工单类型及其自定义字段；已有工单引用时由外键约束或调用检查拒绝并提示改为停用。
type Delete struct {
	db *bun.DB
}

// NewDelete 返回删除工单类型的动作。
func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

// Handle 删除 sub 所在工作区中的工单类型 id 并记录操作日志。不存在时返回 ErrNotFound；被工单引用时返回 ErrInUse。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := Find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model(t).WherePK().Exec(ctx); err != nil {
			if database.IsForeignKeyViolation(err) {
				return ErrInUse.New()
			}
			return errors.Wrap(err, "delete ticket type")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "ticket_type.deleted", TargetType: "ticket_type", TargetID: id,
			Properties: map[string]any{"name": t.Name},
		})
	})
}

// HTTP 处理 DELETE /t/{slug}/api/ticket-types/{id}。
func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
