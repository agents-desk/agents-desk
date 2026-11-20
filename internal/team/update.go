package team

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// UpdateInput 是修改团队收件箱的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body Changes
}

// Update 修改当前工作区中一个团队收件箱的名称、说明和分配方式。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改团队收件箱的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 修改 sub 所在工作区中的团队收件箱 id 并记录操作日志。团队收件箱不存在时返回 ErrNotFound，名称已被使用时返回 ErrNameTaken。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, c Changes) (*TeamDTO, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		var changed []string
		if t.Name != c.Name {
			changed = append(changed, "name")
		}
		if t.Description != c.Description {
			changed = append(changed, "description")
		}
		if t.Assignment != c.assignment() {
			changed = append(changed, "assignment")
		}
		if len(changed) == 0 {
			return nil
		}
		t.Name, t.Description, t.Assignment = c.Name, c.Description, c.assignment()
		if _, err := tx.NewUpdate().Model(t).Column("name", "description", "assignment", "updated_at").WherePK().Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "teams_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", c.Name))
			}
			return errors.Wrap(err, "update team")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "team.updated", TargetType: "team", TargetID: id, Properties: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 PUT /t/{slug}/api/teams/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*TeamOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &TeamOutput{Body: *dto}, nil
}
