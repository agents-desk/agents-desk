package tenant

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// GeneralSettings 是工作区的常规设置。
type GeneralSettings struct {
	Name string `json:"name" minLength:"1" maxLength:"60"`
}

// RenameInput 是修改常规设置的 HTTP 请求。
type RenameInput struct {
	Body GeneralSettings
}

// Rename 修改当前工作区的常规设置：名称。地址（slug）创建后不能修改。
type Rename struct {
	db *bun.DB
}

// NewRename 返回修改工作区名称的动作。
func NewRename(db *bun.DB) *Rename { return &Rename{db: db} }

// Handle 把 sub 所在工作区的名称改为 s.Name，并记录操作日志，返回修改后的当前工作区。
func (a *Rename) Handle(ctx context.Context, sub identity.Subject, s GeneralSettings) (*CurrentDTO, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := find(ctx, tx, sub.Tenant, true)
		if err != nil {
			return err
		}
		if t.Name == s.Name {
			return nil
		}
		from := t.Name
		t.Name, t.UpdatedAt = s.Name, time.Now().UTC()
		if _, err := tx.NewUpdate().Model(t).Column("name", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "rename tenant")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "tenant.renamed", TargetType: "tenant", TargetID: t.ID,
			Properties: map[string]any{"from": from, "to": s.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewCurrent(a.db).Handle(ctx, sub)
}

// HTTP 处理 PUT /t/{slug}/api/tenant。
func (a *Rename) HTTP(ctx context.Context, in *RenameInput) (*CurrentOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &CurrentOutput{Body: *dto}, nil
}
