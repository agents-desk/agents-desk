package attribute

import (
	"context"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// UpdateParams 是修改属性定义的请求：只能修改名称、说明、选项和排序，key 和 type 创建后不变。
type UpdateParams struct {
	Name        string   `json:"name" minLength:"1" maxLength:"60"`
	Description string   `json:"description,omitempty" maxLength:"200"`
	Options     []string `json:"options,omitempty" maxItems:"100" doc:"单选或多选属性的可选项"`
	Position    *int     `json:"position,omitempty" minimum:"0" doc:"展示顺序；不填时保持不变"`
}

// UpdateInput 是修改属性定义的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateParams
}

// Update 修改当前工作区中的一个自定义属性定义。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改属性定义的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 修改 sub 所在工作区中的属性定义 id 并记录操作日志。不存在时返回 ErrNotFound。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, p UpdateParams) (*AttributeDTO, error) {
	name := strings.TrimSpace(p.Name)
	desc := strings.TrimSpace(p.Description)
	var updated *Definition
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		d, err := Find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if err := authorizeManage(sub, d.Target); err != nil {
			return err
		}
		opts, err := normalizeOptions(d.Type, p.Options)
		if err != nil {
			return err
		}
		pos := d.Position
		if p.Position != nil {
			pos = *p.Position
		}
		var changed []string
		if d.Name != name {
			changed = append(changed, "name")
		}
		if d.Description != desc {
			changed = append(changed, "description")
		}
		if !slices.Equal(d.Options, opts) {
			changed = append(changed, "options")
		}
		if d.Position != pos {
			changed = append(changed, "position")
		}
		updated = d
		if len(changed) == 0 {
			return nil
		}
		d.Name, d.Description, d.Options, d.Position = name, desc, opts, pos
		if _, err := tx.NewUpdate().Model(d).Column("name", "description", "options", "position", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update attribute definition")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "attribute.updated", TargetType: "attribute", TargetID: id, Properties: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	dto := updated.DTO()
	return &dto, nil
}

// HTTP 处理 PUT /t/{slug}/api/attributes/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*AttributeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &AttributeOutput{Body: *dto}, nil
}
