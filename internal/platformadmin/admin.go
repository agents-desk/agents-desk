// Package platformadmin 是平台管理员的账号：负责整套部署的人，登录管理员端（/admin）管理工作区和平台设置。
// 邮箱和密码保存在 login 的表中，platform_admins 表只有业务字段；权限是平台的域 "" 上的 authz 角色。
package platformadmin

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// Type 是平台管理员这套账号体系，登录凭据中的主体都是这个类型。
const Type identity.SubjectType = "platform_admin"

// SessionCookie 是平台管理员的会话 cookie，与工作区成员的 __Host-session 分开，两者可以同时登录。
var SessionCookie = httpx.SessionCookie{Name: "__Host-admin"}

var (
	ErrNotFound = errorx.Define(errorx.NotFound, "platform_admin.not_found")
	ErrDisabled = errorx.Define(errorx.Unauthenticated, "platform_admin.disabled")
	ErrSelf     = errorx.Define(errorx.PermissionDenied, "platform_admin.self")
)

// Status 是平台管理员账号的状态。
type Status string

const (
	Active   Status = "active"   // 正常使用
	Disabled Status = "disabled" // 已停用：不能登录
)

// Statuses 是平台管理员的全部状态，显示文字是翻译文件中 platform_admin.status.<状态> 的消息。
var Statuses = enum.New("platform_admin.status", Active, Disabled)

// Schema 让 OpenAPI 中的 Status 只接受上面的取值，并带有显示文字。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

// Admin 是一个平台管理员。
type Admin struct {
	bun.BaseModel `bun:"table:platform_admins"`

	ID           string     `bun:"id,pk"`
	Name         string     `bun:"name,notnull"`
	AvatarFileID *uuid.UUID `bun:"avatar_file_id,type:uuid"` // 头像，files 中的文件；没有头像时为空
	DisabledAt   *time.Time `bun:"disabled_at"`              // 非空表示已停用，不能再登录
	CreatedAt    time.Time  `bun:"created_at,notnull"`
	UpdatedAt    time.Time  `bun:"updated_at,notnull"`
}

// Ref 是这个管理员在 login、apitoken、authz 中的引用。
func (a *Admin) Ref() identity.Ref { return identity.Ref{Type: Type, ID: a.ID} }

// Status 返回平台管理员当前的状态。
func (a *Admin) Status() Status {
	if a.DisabledAt != nil {
		return Disabled
	}
	return Active
}

// AdminDTO 是管理员端管理接口中的平台管理员。
type AdminDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Status    Status    `json:"status"`
	Self      bool      `json:"self" doc:"是否是当前登录的平台管理员"`
	CreatedAt time.Time `json:"created_at"`
}

// AdminOutput 是返回一个平台管理员的响应。
type AdminOutput struct {
	Body AdminDTO
}

// IDInput 按路径参数指定一个平台管理员。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// find 按 ID 读取管理员，不存在时返回 ErrNotFound。
func find(ctx context.Context, db bun.IDB, id string) (*Admin, error) {
	return findLocked(ctx, db, id, false)
}

// findLocked 按 ID 读取管理员，lock 为 true 时用 FOR UPDATE 锁住这一行；不存在时返回 ErrNotFound。
func findLocked(ctx context.Context, db bun.IDB, id string, lock bool) (*Admin, error) {
	a := new(Admin)
	q := db.NewSelect().Model(a).Where("id = ?", id)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return a, errors.Wrap(err, "load platform admin")
}

// emailOf 读取平台管理员绑定的邮箱地址。
func emailOf(ctx context.Context, logins *login.Realm, ref identity.Ref) (string, error) {
	ids, err := logins.Identifiers(ctx, ref)
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		if id.Kind == login.KindEmail {
			return id.Value, nil
		}
	}
	return "", nil
}

// toDTO 把平台管理员模型转换为接口返回结构，viewerID 是当前登录管理员的 ID（用于标记 self）。
func toDTO(ctx context.Context, logins *login.Realm, a *Admin, viewerID string) (AdminDTO, error) {
	email, err := emailOf(ctx, logins, a.Ref())
	if err != nil {
		return AdminDTO{}, err
	}
	return AdminDTO{
		ID: a.ID, Name: a.Name, Email: email, Status: a.Status(),
		Self: a.ID == viewerID, CreatedAt: a.CreatedAt,
	}, nil
}

// Resolve 返回平台管理员的 Resolver：每个请求重新读库，停用立即生效，再按平台的域加载角色中的权限。
func Resolve(db bun.IDB, roles *authz.Store) identity.Resolver {
	return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
		a, err := find(ctx, db, sub.ID)
		if errors.Is(err, ErrNotFound) || err == nil && a.DisabledAt != nil {
			return identity.Subject{}, ErrDisabled.New()
		}
		if err != nil {
			return identity.Subject{}, err
		}
		return roles.Resolve(ctx, sub)
	}
}
