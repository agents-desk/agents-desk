package attribute

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

// CreateParams 是新建属性定义的请求。
type CreateParams struct {
	Target      Target   `json:"target" doc:"属性所属对象（conversation 或 contact）"`
	Key         string   `json:"key" minLength:"1" maxLength:"64" pattern:"^[a-z][a-z0-9_]{0,63}$" doc:"属性标识，小写字母开头，可包含小写字母、数字和下划线；创建后不可修改"`
	Name        string   `json:"name" minLength:"1" maxLength:"60"`
	Description string   `json:"description,omitempty" maxLength:"200"`
	Type        Type     `json:"type" doc:"属性的数据类型；创建后不可修改"`
	Options     []string `json:"options,omitempty" maxItems:"100" doc:"单选或多选属性的可选项"`
	Position    *int     `json:"position,omitempty" minimum:"0" doc:"展示顺序，不填时排在最后"`
}

// CreateInput 是新建属性定义的 HTTP 请求。
type CreateInput struct {
	Body CreateParams
}

// Create 在当前工作区中新建自定义属性定义。
type Create struct {
	db *bun.DB
}

// NewCreate 返回新建属性定义的动作。
func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 在 sub 所在的工作区中新建自定义属性定义并记录操作日志。
// 同一个对象下 key 已被使用时返回 ErrKeyTaken；单选或多选属性缺少选项时返回 ErrOptionsRequired。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, p CreateParams) (*AttributeDTO, error) {
	opts, err := normalizeOptions(p.Type, p.Options)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(p.Name)
	desc := strings.TrimSpace(p.Description)
	d := &Definition{
		ID: newID(), TenantID: sub.Tenant, Target: p.Target, Key: p.Key,
		Name: name, Description: desc, Type: p.Type, Options: opts,
	}
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if p.Position != nil {
			d.Position = *p.Position
		} else {
			var next int
			if err := tx.NewRaw(`SELECT COALESCE(MAX(position) + 1, 0) FROM attribute_definitions WHERE tenant_id = ? AND target = ?`,
				sub.Tenant, p.Target).Scan(ctx, &next); err != nil {
				return errors.Wrap(err, "next attribute position")
			}
			d.Position = next
		}
		if _, err := tx.NewInsert().Model(d).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "attribute_definitions_tenant_target_key") {
				return ErrKeyTaken.New(errorx.Field("key"), errorx.Public("key", p.Key))
			}
			return errors.Wrap(err, "insert attribute definition")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "attribute.created", TargetType: "attribute", TargetID: d.ID,
			Properties: map[string]any{"target": string(d.Target), "key": d.Key},
		})
	})
	if err != nil {
		return nil, err
	}
	dto := d.DTO()
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/attributes。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*AttributeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &AttributeOutput{Body: *dto}, nil
}
