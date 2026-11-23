// Package member 是工作区成员的账号。账号是全局的：一个人登录一次，可以属于多个租户（工作区），
// 他在每个租户中的角色保存在 authz 中。邮箱和密码保存在 login 的表中，members 表只有业务字段。
package member

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
)

// Type 是工作区成员这套账号体系。
const Type identity.SubjectType = "member"

var (
	ErrNotFound       = errorx.Define(errorx.NotFound, "member.not_found")
	ErrDisabled       = errorx.Define(errorx.Conflict, "member.disabled")
	ErrDetailsMissing = errorx.Define(errorx.InvalidArgument, "member.details_missing")
	// ErrAccountDisabled 是登录或调用接口的成员账号已停用（或已删除）：响应 401，前端回到登录页。
	ErrAccountDisabled = errorx.Define(errorx.Unauthenticated, "member.account_disabled")
)

// SessionCookie 是成员的会话 cookie：普通登录用默认的 __Host-session，代为登录用独立的 __Host-impersonation，
// 两者与平台管理员的 __Host-admin 互不覆盖；退出代为登录时仅清除 __Host-impersonation，保留原有登录态。
var SessionCookie = httpx.SessionCookie{Impersonation: "__Host-impersonation"}

// Member 是一个成员账号。
type Member struct {
	bun.BaseModel `bun:"table:members"`

	ID                      string                  `bun:"id,pk"`
	Name                    string                  `bun:"name,notnull"`
	AvatarFileID            *uuid.UUID              `bun:"avatar_file_id,type:uuid"`                    // 头像，files 中的文件；没有头像时为空
	Language                Language                `bun:"language,notnull"`                            // 语言，见 Preferences
	TimeZone                string                  `bun:"time_zone,notnull"`                           // IANA 时区名，为空表示跟随浏览器
	SendShortcut            SendShortcut            `bun:"send_shortcut,notnull"`                       // 回复框发送快捷键：enter 或 ctrl_enter
	OpenNextAfterReply      bool                    `bun:"open_next_after_reply,notnull"`               // 发送回复后是否自动打开下一个待回复会话
	NotificationPreferences NotificationPreferences `bun:"notification_preferences,type:jsonb,notnull"` // 实时提醒的浏览器通知与提示音开关
	DisabledAt              *time.Time              `bun:"disabled_at"`                                 // 非空表示已停用，不能再登录任何工作区
	CreatedAt               time.Time               `bun:"created_at,notnull"`
	UpdatedAt               time.Time               `bun:"updated_at,notnull"`
}

// Ref 是这个成员在 login、apitoken、authz 中的引用。
func (m *Member) Ref() identity.Ref { return identity.Ref{Type: Type, ID: m.ID} }

// Find 按 ID 读取成员，不存在时返回 ErrNotFound。
func Find(ctx context.Context, db bun.IDB, id string) (*Member, error) {
	m := new(Member)
	err := db.NewSelect().Model(m).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return m, errors.Wrap(err, "load member")
}

// Resolve 返回成员的 Resolver：每个请求重新读库，账号停用立即生效（ErrAccountDisabled），再按本次请求的租户
// 加载成员在其中的权限：角色的权限加上每位成员都有的基本权限（auth.WithBase）。租户是平台本身（""）时权限为空；
// 成员在租户中没有角色时 roles 返回 404。
func Resolve(db bun.IDB, roles *authz.Store) identity.Resolver {
	return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
		m, err := Find(ctx, db, sub.ID)
		if errors.Is(err, ErrNotFound) || err == nil && m.DisabledAt != nil {
			return identity.Subject{}, ErrAccountDisabled.New()
		}
		if err != nil {
			return identity.Subject{}, err
		}
		resolved, err := roles.Resolve(ctx, sub)
		if err != nil || resolved.Tenant == "" {
			return resolved, err
		}
		resolved.Permissions = auth.WithBase(resolved.Permissions)
		return resolved, nil
	}
}

// Email 返回成员登录用的邮箱，没有邮箱时返回空字符串。
func Email(ctx context.Context, logins *login.Realm, ref identity.Ref) (string, error) {
	ids, err := logins.Identifiers(ctx, ref)
	if err != nil {
		return "", err
	}
	if i := slices.IndexFunc(ids, func(id login.Attached) bool { return id.Kind == login.KindEmail }); i >= 0 {
		return ids[i].Value, nil
	}
	return "", nil
}

// Account 是按邮箱指定的成员账号：邮箱已有账号时使用它，Name 和 Password 不使用；没有时用它们创建新账号。
type Account struct {
	Email    string
	Name     string
	Password string // 新账号的初始密码，由管理员设置后告诉本人
}

// Ensure 返回邮箱为 acc.Email 的成员，没有时在 tx 中创建：写入 members、邮箱（标为已验证）和初始密码。
// 已有的账号停用时返回 ErrDisabled；需要新建而缺少姓名或密码时返回 ErrDetailsMissing；密码不符合要求时返回 login.ErrWeakPassword。
// logins 是成员的 login.Realm。
func Ensure(ctx context.Context, tx bun.IDB, logins *login.Realm, acc Account) (*Member, error) {
	ref, err := logins.Lookup(ctx, login.Email(acc.Email))
	switch {
	case err == nil:
		m, err := Find(ctx, tx, ref.ID)
		if err != nil {
			return nil, err
		}
		if m.DisabledAt != nil {
			return nil, ErrDisabled.New()
		}
		return m, nil
	case !errors.Is(err, login.ErrNotFound):
		return nil, err
	}

	if acc.Name == "" || acc.Password == "" {
		return nil, ErrDetailsMissing.New()
	}
	now := time.Now().UTC()
	m := &Member{
		ID:                      uuid.Must(uuid.NewV7()).String(),
		Name:                    acc.Name,
		Language:                SimplifiedChinese,
		SendShortcut:            SendEnter,
		NotificationPreferences: DefaultNotificationPreferences(),
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if _, err := tx.NewInsert().Model(m).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "insert member")
	}
	if err := logins.Attach(ctx, tx, m.Ref(), login.Email(acc.Email), true); err != nil {
		return nil, err
	}
	if err := logins.SetPassword(ctx, tx, m.Ref(), acc.Password); err != nil {
		return nil, err
	}
	return m, nil
}
