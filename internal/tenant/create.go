package tenant

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/automation"
	"agents-desk/internal/member"
	"agents-desk/internal/role"
)

// CreateParams 是新建租户的内容：名称、slug 和主管理员。主管理员的邮箱已有成员账号时，姓名和初始密码不使用。
type CreateParams struct {
	Name          string `json:"name" minLength:"1" maxLength:"60"`
	Slug          string `json:"slug" pattern:"^[a-z][a-z0-9-]{1,30}[a-z0-9]$" doc:"工作区的地址 /t/{slug}/：小写字母开头，小写字母、数字和连字符，3 到 32 个字符；创建后不能修改"`
	OwnerEmail    string `json:"owner_email" format:"email" maxLength:"254"`
	OwnerName     string `json:"owner_name,omitempty" maxLength:"60" doc:"这个邮箱还没有成员账号时必填"`
	OwnerPassword string `json:"owner_password,omitempty" maxLength:"128" doc:"新账号的初始密码，这个邮箱还没有成员账号时必填"`
}

// CreateInput 是新建租户的请求。
type CreateInput struct {
	Body CreateParams
}

// Create 新建租户并指定主管理员：主管理员的邮箱没有成员账号时同时创建账号。
type Create struct {
	db     *bun.DB
	logins *login.Realm // 成员的 login.Realm
	addr   Addresses
}

// NewCreate 返回新建租户的动作。
func NewCreate(db *bun.DB, logins *login.Realm, addr Addresses) *Create {
	return &Create{db: db, logins: logins, addr: addr}
}

// Handle 在一个事务中写入租户、找到或创建主管理员的账号、分配 owner 角色、创建初始角色（role.Defaults），并记录操作日志，
// 任何一步失败时整体回滚。
// slug 是保留词时返回 ErrSlugReserved，已被使用时返回 ErrSlugTaken；主管理员的错误见 member.Ensure。
func (a *Create) Handle(ctx context.Context, subject identity.Subject, p CreateParams) (*Tenant, error) {
	if err := checkSlug(p.Slug); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond) // PostgreSQL 只保存到微秒：创建后返回的值与之后读出的相同
	t := &Tenant{ID: newID(), Slug: p.Slug, Name: p.Name, CreatedAt: now, UpdatedAt: now}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		owner, err := member.Ensure(ctx, tx, a.logins, member.Account{Email: p.OwnerEmail, Name: p.OwnerName, Password: p.OwnerPassword})
		if err != nil {
			return err
		}
		t.OwnerID, t.Owner = owner.ID, owner
		if _, err := tx.NewInsert().Model(t).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "tenants_slug_unique") {
				return ErrSlugTaken.New(errorx.Public("slug", p.Slug))
			}
			return errors.Wrap(err, "insert tenant")
		}
		if err := auth.Grant(ctx, authz.NewStore(tx, auth.Catalog), owner.Ref(), t.ID, auth.Owner); err != nil {
			return err
		}
		if err := role.CreateDefaults(ctx, tx, t.ID); err != nil {
			return err
		}
		if err := automation.EnsureBuiltins(ctx, tx, t.ID); err != nil {
			return err
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "tenant.created", TargetType: "tenant", TargetID: t.ID,
			Properties: map[string]any{"name": t.Name, "slug": t.Slug, "owner_id": owner.ID},
		})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// HTTP 处理 POST /tenants。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*TenantOutput, error) {
	subject, _ := identity.FromContext(ctx)
	t, err := a.Handle(ctx, subject, in.Body)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.logins, a.addr, t)
	if err != nil {
		return nil, err
	}
	return &TenantOutput{Body: dto}, nil
}
