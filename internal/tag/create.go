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

// Changes 是新建或修改标签的请求：名称、颜色和说明。
type Changes struct {
	Name        string `json:"name" minLength:"1" maxLength:"40"`
	Color       Color  `json:"color,omitempty" doc:"标签的颜色，不填时是灰蓝"`
	Description string `json:"description,omitempty" maxLength:"200"`
}

// color 返回 c 的颜色，没有填写时是默认灰蓝。
func (c Changes) color() Color {
	if c.Color == "" {
		return BlueGray
	}
	return c.Color
}

// CreateInput 是新建标签的 HTTP 请求。
type CreateInput struct {
	Body Changes
}

// Create 在当前工作区中新建标签。
type Create struct {
	db *bun.DB
}

// NewCreate 返回新建标签的动作。
func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 在 sub 所在的工作区中新建标签并记录操作日志。名称已被使用时返回 ErrNameTaken（字段 name）。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, c Changes) (*TagDTO, error) {
	name := strings.TrimSpace(c.Name)
	desc := strings.TrimSpace(c.Description)
	t := &Tag{ID: newID(), TenantID: sub.Tenant, Name: name, Color: c.color(), Description: desc}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(t).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "tags_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", name))
			}
			return errors.Wrap(err, "insert tag")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{Event: "tag.created", TargetType: "tag", TargetID: t.ID})
	})
	if err != nil {
		return nil, err
	}
	return &TagDTO{ID: t.ID, Name: t.Name, Color: t.Color, Description: t.Description}, nil
}

// HTTP 处理 POST /t/{slug}/api/tags。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*TagOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TagOutput{Body: *dto}, nil
}
