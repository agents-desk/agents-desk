package organization

import (
	"context"
	"database/sql"
	"maps"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// UpdateBody 是修改客户公司/组织的请求体：只更新传入的非 nil 字段。
type UpdateBody struct {
	Name       *string        `json:"name,omitempty" minLength:"1" maxLength:"120" doc:"组织名称"`
	Domain     *string        `json:"domain,omitempty" maxLength:"200" doc:"主域名"`
	ExternalID *string        `json:"external_id,omitempty" maxLength:"120" doc:"业务系统外部客户编号"`
	Notes      *string        `json:"notes,omitempty" maxLength:"4000" doc:"组织备注"`
	Attributes map[string]any `json:"attributes,omitempty" doc:"扩展属性（覆盖）"`
}

// UpdateInput 是修改组织的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateBody
}

// Update 修改工作区中的客户公司/组织资料。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改组织的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 更新工作区中组织 id 的资料并记录操作日志；同名或同外部编号冲突时返回明确字段错误。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, b UpdateBody) (*DTO, error) {
	var updated *Organization
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		o := new(Organization)
		err := tx.NewSelect().Model(o).Where("org.id = ? AND org.tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "lock organization for update")
		}
		var cols, changed []string
		if b.Name != nil {
			v := strings.TrimSpace(*b.Name)
			if v != o.Name {
				o.Name, cols, changed = v, append(cols, "name"), append(changed, "name")
			}
		}
		if b.Domain != nil {
			v := normalizeDomain(*b.Domain)
			if v != o.Domain {
				o.Domain, cols, changed = v, append(cols, "domain"), append(changed, "domain")
			}
		}
		if b.ExternalID != nil {
			v := strings.TrimSpace(*b.ExternalID)
			if v != o.ExternalID {
				o.ExternalID, cols, changed = v, append(cols, "external_id"), append(changed, "external_id")
			}
		}
		if b.Notes != nil {
			v := strings.TrimSpace(*b.Notes)
			if v != o.Notes {
				o.Notes, cols, changed = v, append(cols, "notes"), append(changed, "notes")
			}
		}
		if b.Attributes != nil {
			o.Attributes = maps.Clone(b.Attributes)
			cols, changed = append(cols, "attributes"), append(changed, "attributes")
		}
		if len(cols) == 0 {
			updated = o
			return nil
		}
		o.UpdatedAt = now()
		if _, err := tx.NewUpdate().Model(o).Column(append(cols, "updated_at")...).WherePK().Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "organizations_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", o.Name))
			}
			if database.IsUniqueViolation(err, "organizations_tenant_external_id") {
				return ErrExternalIDTaken.New(errorx.Field("external_id"), errorx.Public("external_id", o.ExternalID))
			}
			return errors.Wrap(err, "update organization")
		}
		updated = o
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event:      "organization.updated",
			TargetType: "organization",
			TargetID:   o.ID,
			Properties: map[string]any{"name": o.Name, "changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	dtos, err := buildDTOs(ctx, a.db, sub.Tenant, []Organization{*updated})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 PUT /t/{slug}/api/organizations/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*Output, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &Output{Body: *dto}, nil
}
