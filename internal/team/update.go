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

// UpdateInput 是修改团队的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body Changes
}

// Update 修改当前工作区中一个团队的名称和说明。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改团队的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 修改 sub 所在工作区中的团队 id 并记录操作日志。团队不存在时返回 ErrNotFound，名称已被使用时返回 ErrNameTaken。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, c Changes) (*TeamDTO, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if t.Name == c.Name && t.Description == c.Description {
			return nil
		}
		t.Name, t.Description = c.Name, c.Description
		if _, err := tx.NewUpdate().Model(t).Column("name", "description", "updated_at").WherePK().Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "teams_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", c.Name))
			}
			return errors.Wrap(err, "update team")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{Event: "team.updated", TargetType: "team", TargetID: id})
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
