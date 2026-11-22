package tag

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// UpdateInput 是修改标签的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body Changes
}

// Update 修改当前工作区中一个标签的名称、颜色和说明。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改标签的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 修改 sub 所在工作区中的标签 id 并记录操作日志。标签不存在时返回 ErrNotFound，名称已被使用时返回 ErrNameTaken。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, c Changes) (*TagDTO, error) {
	name := strings.TrimSpace(c.Name)
	desc := strings.TrimSpace(c.Description)
	color := c.color()
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := Find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		var changed []string
		if t.Name != name {
			changed = append(changed, "name")
		}
		if t.Color != color {
			changed = append(changed, "color")
		}
		if t.Description != desc {
			changed = append(changed, "description")
		}
		if len(changed) == 0 {
			return nil
		}
		t.Name, t.Color, t.Description = name, color, desc
		if _, err := tx.NewUpdate().Model(t).Column("name", "color", "description", "updated_at").WherePK().Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "tags_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", name))
			}
			return errors.Wrap(err, "update tag")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "tag.updated", TargetType: "tag", TargetID: id, Properties: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 PUT /t/{slug}/api/tags/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*TagOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &TagOutput{Body: *dto}, nil
}
