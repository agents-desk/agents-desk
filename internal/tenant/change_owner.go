package tenant

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// OwnerParams 是新的主管理员。邮箱已有成员账号时，姓名和初始密码不使用。
type OwnerParams struct {
	Email    string `json:"email" format:"email" maxLength:"254"`
	Name     string `json:"name,omitempty" maxLength:"60" doc:"这个邮箱还没有成员账号时必填"`
	Password string `json:"password,omitempty" maxLength:"128" doc:"新账号的初始密码，这个邮箱还没有成员账号时必填"`
}

// ChangeOwnerInput 是更换主管理员的请求。
type ChangeOwnerInput struct {
	ID   string `path:"id" format:"uuid"`
	Body OwnerParams
}

// ChangeOwner 更换租户的主管理员：撤销原主管理员的 owner 角色，分配给新的主管理员。每位成员只有一个角色，
// 新主管理员原来是成员时，他原来的角色被撤销；原主管理员也就不再是这个租户的成员。
type ChangeOwner struct {
	db     *bun.DB
	logins *login.Realm // 成员的 login.Realm
	addr   Addresses
}

// NewChangeOwner 返回更换主管理员的动作。
func NewChangeOwner(db *bun.DB, logins *login.Realm, addr Addresses) *ChangeOwner {
	return &ChangeOwner{db: db, logins: logins, addr: addr}
}

// Handle 在一个事务中锁住租户、找到或创建新主管理员的账号、调整 owner 角色和 owner_id，并记录操作日志。
// 新主管理员就是原主管理员时什么也不做。租户不存在时返回 ErrNotFound；新主管理员的错误见 member.Ensure。
func (a *ChangeOwner) Handle(ctx context.Context, subject identity.Subject, id string, p OwnerParams) (*Tenant, error) {
	var t *Tenant
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if t, err = find(ctx, tx, id, true); err != nil {
			return err
		}
		owner, err := member.Ensure(ctx, tx, a.logins, member.Account{Email: p.Email, Name: p.Name, Password: p.Password})
		if err != nil {
			return err
		}
		if owner.ID == t.OwnerID {
			return nil
		}
		roles := authz.NewStore(tx, auth.Catalog)
		if err := auth.Revoke(ctx, roles, t.Owner.Ref(), t.ID, auth.Owner); err != nil {
			return err
		}
		if _, err := roles.RemoveMember(ctx, owner.Ref(), t.ID); err != nil {
			return err
		}
		if err := auth.Grant(ctx, roles, owner.Ref(), t.ID, auth.Owner); err != nil {
			return err
		}
		previous := t.OwnerID
		t.OwnerID, t.Owner, t.UpdatedAt = owner.ID, owner, time.Now().UTC()
		if _, err := tx.NewUpdate().Model(t).Column("owner_id", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update tenant owner")
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "tenant.owner_changed", TargetType: "tenant", TargetID: t.ID,
			Properties: map[string]any{"from": previous, "to": owner.ID},
		})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// HTTP 处理 PUT /tenants/{id}/owner。
func (a *ChangeOwner) HTTP(ctx context.Context, in *ChangeOwnerInput) (*TenantOutput, error) {
	subject, _ := identity.FromContext(ctx)
	t, err := a.Handle(ctx, subject, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.logins, a.addr, t)
	if err != nil {
		return nil, err
	}
	return &TenantOutput{Body: dto}, nil
}
