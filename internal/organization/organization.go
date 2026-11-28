// Package organization 是客户公司/组织：工作区内按企业主体聚合联系人与外部协作群的一等实体。
package organization

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrNotFound 是客户公司/组织不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "organization.not_found")
	// ErrNameTaken 是同工作区内已存在相同名称的客户公司/组织（不区分大小写）。
	ErrNameTaken = errorx.Define(errorx.Conflict, "organization.name_taken")
	// ErrExternalIDTaken 是同工作区内已存在相同外部客户编号的组织。
	ErrExternalIDTaken = errorx.Define(errorx.Conflict, "organization.external_id_taken")
)

// Organization 是 organizations 表的数据库模型，属于一个工作区。
type Organization struct {
	bun.BaseModel `bun:"table:organizations,alias:org"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID   string         `bun:"tenant_id,notnull"`
	Name       string         `bun:"name,notnull"`
	Domain     string         `bun:"domain,notnull"`
	ExternalID string         `bun:"external_id,notnull"`
	Notes      string         `bun:"notes,notnull"`
	Attributes map[string]any `bun:"attributes,type:jsonb,notnull"`
}

// Summary 是嵌入联系人或外部群响应中的轻量组织概要。
type Summary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Domain     string `json:"domain,omitempty" doc:"组织主域名"`
	ExternalID string `json:"external_id,omitempty" doc:"外部客户编号"`
}

// Summary 返回组织 o 的轻量概要信息。
func (o *Organization) Summary() Summary {
	return Summary{
		ID:         o.ID,
		Name:       o.Name,
		Domain:     o.Domain,
		ExternalID: o.ExternalID,
	}
}

// DTO 是组织管理列表与详情返回的完整客户公司视图。
type DTO struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Domain             string         `json:"domain"`
	ExternalID         string         `json:"external_id"`
	Notes              string         `json:"notes"`
	Attributes         map[string]any `json:"attributes"`
	ContactCount       int            `json:"contact_count" doc:"关联的联系人数量"`
	ExternalGroupCount int            `json:"external_group_count" doc:"关联的外部协作群数量"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// Output 是返回单个客户组织的 HTTP 响应。
type Output struct {
	Body DTO
}

// Find 读取工作区 tenant 中的组织 id；不存在或属于其他工作区时返回 ErrNotFound。
func Find(ctx context.Context, db bun.IDB, tenant, id string) (*Organization, error) {
	o := new(Organization)
	err := db.NewSelect().Model(o).Where("org.id = ? AND org.tenant_id = ?", id, tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return o, errors.Wrap(err, "load organization")
}

// Summaries 批量加载工作区 tenant 中 ids 对应的组织概要，供联系人与外部群 DTO 一次回填。
func Summaries(ctx context.Context, db bun.IDB, tenant string, ids []string) (map[string]Summary, error) {
	out := map[string]Summary{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []Organization
	if err := db.NewSelect().Model(&rows).
		Where("org.tenant_id = ? AND org.id IN (?)", tenant, bun.In(ids)).
		Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load organization summaries")
	}
	for i := range rows {
		out[rows[i].ID] = rows[i].Summary()
	}
	return out, nil
}

// buildDTOs 为一批组织记录批量聚合联系人数与外部群数，构造 DTO 列表。
func buildDTOs(ctx context.Context, db bun.IDB, tenant string, rows []Organization) ([]DTO, error) {
	out := make([]DTO, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}

	var contactCounts []struct {
		OrgID string `bun:"organization_id"`
		Cnt   int    `bun:"cnt"`
	}
	if err := db.NewSelect().Table("contacts").
		Column("organization_id").ColumnExpr("COUNT(*) AS cnt").
		Where("tenant_id = ? AND organization_id IN (?)", tenant, bun.In(ids)).
		GroupExpr("organization_id").
		Scan(ctx, &contactCounts); err != nil {
		return nil, errors.Wrap(err, "count organization contacts")
	}
	byContact := make(map[string]int, len(contactCounts))
	for _, r := range contactCounts {
		byContact[r.OrgID] = r.Cnt
	}

	var groupCounts []struct {
		OrgID string `bun:"organization_id"`
		Cnt   int    `bun:"cnt"`
	}
	if err := db.NewSelect().Table("external_groups").
		Column("organization_id").ColumnExpr("COUNT(*) AS cnt").
		Where("tenant_id = ? AND organization_id IN (?)", tenant, bun.In(ids)).
		GroupExpr("organization_id").
		Scan(ctx, &groupCounts); err != nil {
		return nil, errors.Wrap(err, "count organization external groups")
	}
	byGroup := make(map[string]int, len(groupCounts))
	for _, r := range groupCounts {
		byGroup[r.OrgID] = r.Cnt
	}

	for i := range rows {
		o := &rows[i]
		attrs := o.Attributes
		if attrs == nil {
			attrs = map[string]any{}
		}
		out[i] = DTO{
			ID:                 o.ID,
			Name:               o.Name,
			Domain:             o.Domain,
			ExternalID:         o.ExternalID,
			Notes:              o.Notes,
			Attributes:         attrs,
			ContactCount:       byContact[o.ID],
			ExternalGroupCount: byGroup[o.ID],
			CreatedAt:          o.CreatedAt,
			UpdatedAt:          o.UpdatedAt,
		}
	}
	return out, nil
}

// normalizeDomain 将输入的域名或 URL 规范化为小写纯主机名。
func normalizeDomain(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
			s = u.Hostname()
		}
	} else if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func newID() string  { return uuid.Must(uuid.NewV7()).String() }
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
