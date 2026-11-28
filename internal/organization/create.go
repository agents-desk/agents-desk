package organization

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// CreateBody 是新建客户公司/组织的请求体。
type CreateBody struct {
	Name       string         `json:"name" minLength:"1" maxLength:"120" doc:"组织名称"`
	Domain     string         `json:"domain,omitempty" maxLength:"200" doc:"主域名（如 acme.com）"`
	ExternalID string         `json:"external_id,omitempty" maxLength:"120" doc:"业务系统外部客户编号"`
	Notes      string         `json:"notes,omitempty" maxLength:"4000" doc:"组织备注"`
	Attributes map[string]any `json:"attributes,omitempty" doc:"扩展属性"`
}

// CreateInput 是新建组织的 HTTP 请求。
type CreateInput struct {
	Body CreateBody
}

// Create 在工作区中新建客户公司/组织。
type Create struct {
	db *bun.DB
}

// NewCreate 返回新建组织的动作。
func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 在 sub 所在工作区中新建组织并记录操作日志。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, b CreateBody) (*DTO, error) {
	at := database.Now()
	attrs := b.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	o := &Organization{
		ID:         database.NewID(),
		TenantID:   sub.Tenant,
		Name:       strings.TrimSpace(b.Name),
		Domain:     normalizeDomain(b.Domain),
		ExternalID: strings.TrimSpace(b.ExternalID),
		Notes:      strings.TrimSpace(b.Notes),
		Attributes: attrs,
	}
	o.CreatedAt, o.UpdatedAt = at, at
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(o).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "organizations_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", o.Name))
			}
			if database.IsUniqueViolation(err, "organizations_tenant_external_id") {
				return ErrExternalIDTaken.New(errorx.Field("external_id"), errorx.Public("external_id", o.ExternalID))
			}
			return errors.Wrap(err, "insert organization")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event:      "organization.created",
			TargetType: "organization",
			TargetID:   o.ID,
			Properties: map[string]any{"name": o.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "created organization", "tenant_id", sub.Tenant, "organization_id", o.ID, "name", o.Name)
	dtos, err := buildDTOs(ctx, a.db, sub.Tenant, []Organization{*o})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 POST /t/{slug}/api/organizations。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*Output, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &Output{Body: *dto}, nil
}
