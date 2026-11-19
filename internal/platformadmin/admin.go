// Package platformadmin 是平台管理员的账号：负责整套部署的人，登录管理员端（/admin）管理工作区和平台设置。
// 邮箱和密码保存在 login 的表中，platform_admins 表只有业务字段；权限是平台的域 "" 上的 authz 角色。
package platformadmin

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
)

// Type 是平台管理员这套账号体系，登录凭据中的主体都是这个类型。
const Type identity.SubjectType = "platform_admin"

// SessionCookie 是平台管理员的会话 cookie，与工作区成员的 __Host-session 分开，两者可以同时登录。
var SessionCookie = httpx.SessionCookie{Name: "__Host-admin"}

var (
	ErrNotFound = errorx.Define(errorx.NotFound, "platform_admin.not_found")
	ErrDisabled = errorx.Define(errorx.Unauthenticated, "platform_admin.disabled")
)

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

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// find 按 ID 读取管理员，不存在时返回 ErrNotFound。
func find(ctx context.Context, db bun.IDB, id string) (*Admin, error) {
	a := new(Admin)
	err := db.NewSelect().Model(a).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return a, errors.Wrap(err, "load platform admin")
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
