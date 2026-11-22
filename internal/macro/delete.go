package macro

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// Delete 删除当前工作区中的一条快捷回复。
type Delete struct {
	db *bun.DB
}

// NewDelete 返回删除快捷回复的动作。
func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

// Handle 由 sub 删除快捷回复 id。共享快捷回复要求 macros:manage 权限并记录操作日志；
// 个人快捷回复只允许本人删除。不存在或不可见返回 ErrNotFound，无权管理返回 ErrForbidden。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		m, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if !canManage(sub, m) {
			return ErrForbidden.New()
		}
		if _, err := tx.NewDelete().Model(m).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "delete macro")
		}
		if m.OwnerID == nil {
			return activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "macro.deleted",
				TargetType: "macro",
				TargetID:   m.ID,
				Properties: map[string]any{"title": m.Title, "shortcut": m.Shortcut},
			})
		}
		return nil
	})
}

// HTTP 处理 DELETE /t/{slug}/api/macros/{id}。
func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
