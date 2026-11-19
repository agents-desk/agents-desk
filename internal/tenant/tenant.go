// Package tenant 是租户，即产品中的“工作区”：一个团队的空间，包含它的成员、数据和配置。
// 租户的 ID 是 Tenon 中的租户（identity.Subject.Tenant）、authz 的域，也是业务表中的 tenant_id。
//
// 身份和地址分开：ID 是 UUID，只在代码和数据中使用；slug 是地址（/t/{slug}/），以后可以再加自定义域名。
// 请求只在一处由地址换成 ID，业务代码只见到 ID。
package tenant

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/member"
)

var (
	ErrNotFound     = errorx.Define(errorx.NotFound, "tenant.not_found")
	ErrSlugTaken    = errorx.Define(errorx.Conflict, "tenant.slug_taken")
	ErrSlugReserved = errorx.Define(errorx.InvalidArgument, "tenant.slug_reserved")
	// ErrDisabled 是成员进入的工作区已停用（403）：只有这个工作区的成员会看到它，其他人得到 404。
	ErrDisabled = errorx.Define(errorx.PermissionDenied, "tenant.disabled")
)

// reservedSlugs 是不能作为 slug 的词：它们是或者将来可能是应用自己的路径。
var reservedSlugs = []string{"admin", "api", "app", "assets", "help", "login", "logout", "static", "support", "system", "widget", "www"}

// Status 是租户的状态。
type Status string

const (
	Active   Status = "active"   // 正常使用
	Disabled Status = "disabled" // 已停用：成员不能进入，数据保留
)

// Statuses 是租户的全部状态，显示文字是翻译文件中 tenant.status.<状态> 的消息。
var Statuses = enum.New("tenant.status", Active, Disabled)

// Schema 让 OpenAPI 中的 Status 只接受上面的取值，并带有显示文字。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

// Tenant 是一个租户。主管理员在 owner_id 中，同时在 authz 中拥有这个租户的 owner 角色，两者在同一个事务中修改。
type Tenant struct {
	bun.BaseModel `bun:"table:tenants"`

	ID         string         `bun:"id,pk"`
	Slug       string         `bun:"slug,notnull"`
	Name       string         `bun:"name,notnull"`
	OwnerID    string         `bun:"owner_id,notnull"`
	Owner      *member.Member `bun:"rel:belongs-to,join:owner_id=id"`
	DisabledAt *time.Time     `bun:"disabled_at"` // 非空表示已停用
	CreatedAt  time.Time      `bun:"created_at,notnull"`
	UpdatedAt  time.Time      `bun:"updated_at,notnull"`
}

// Status 返回租户当前的状态。
func (t *Tenant) Status() Status {
	if t.DisabledAt != nil {
		return Disabled
	}
	return Active
}

// TenantDTO 是接口中的租户，带有主管理员的名字和邮箱。
type TenantDTO struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	URL        string    `json:"url" doc:"成员进入工作区的地址"`
	Status     Status    `json:"status"`
	OwnerID    string    `json:"owner_id"`
	OwnerName  string    `json:"owner_name"`
	OwnerEmail string    `json:"owner_email"`
	CreatedAt  time.Time `json:"created_at"`
}

// TenantOutput 是返回一个租户的响应。
type TenantOutput struct {
	Body TenantDTO
}

// Addresses 生成租户的地址。地址只由这里生成，以后有了自定义域名，只改这一处。
type Addresses struct {
	PublicURL string // 应用对外的地址，例如 https://desk.example.com
}

// URL 返回成员进入租户的地址：<PublicURL>/t/<slug>/。slug 只含小写字母、数字和连字符，不需要转义。
func (a Addresses) URL(t *Tenant) string {
	return strings.TrimSuffix(a.PublicURL, "/") + "/t/" + t.Slug + "/"
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// checkSlug 检查 slug 不是保留词；格式由接口的 pattern 校验。
func checkSlug(slug string) error {
	if slices.Contains(reservedSlugs, slug) {
		return ErrSlugReserved.New(errorx.Public("slug", slug))
	}
	return nil
}

// find 按 ID 读取租户和它的主管理员，不存在时返回 ErrNotFound。lock 为 true 时用 FOR UPDATE 锁住这一行。
func find(ctx context.Context, db bun.IDB, id string, lock bool) (*Tenant, error) {
	t := new(Tenant)
	q := db.NewSelect().Model(t).Where("tenant.id = ?", id)
	if lock {
		q = q.For("UPDATE OF tenant")
	}
	err := q.Relation("Owner").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return t, errors.Wrap(err, "load tenant")
}

// toDTO 把租户转换为接口中的形式，按 logins（成员的 login.Realm）读取主管理员的邮箱。t.Owner 必须已加载。
func toDTO(ctx context.Context, logins *login.Realm, addr Addresses, t *Tenant) (TenantDTO, error) {
	email, err := member.Email(ctx, logins, t.Owner.Ref())
	if err != nil {
		return TenantDTO{}, err
	}
	return TenantDTO{
		ID: t.ID, Name: t.Name, Slug: t.Slug, URL: addr.URL(t), Status: t.Status(),
		OwnerID: t.OwnerID, OwnerName: t.Owner.Name, OwnerEmail: email, CreatedAt: t.CreatedAt,
	}, nil
}
