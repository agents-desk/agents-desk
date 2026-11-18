package tenant

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// SetDisabled 停用或启用租户。停用后成员不能进入这个工作区，数据保留，可以随时启用。
type SetDisabled struct {
	db       *bun.DB
	logins   *login.Realm // 成员的 login.Realm，读取主管理员的邮箱
	addr     Addresses
	disabled bool // true 是停用，false 是启用
}

// NewDisable 返回停用租户的动作。
func NewDisable(db *bun.DB, logins *login.Realm, addr Addresses) *SetDisabled {
	return &SetDisabled{db: db, logins: logins, addr: addr, disabled: true}
}

// NewEnable 返回启用租户的动作。
func NewEnable(db *bun.DB, logins *login.Realm, addr Addresses) *SetDisabled {
	return &SetDisabled{db: db, logins: logins, addr: addr, disabled: false}
}

// Handle 停用或启用租户 id 并记录操作日志。已经是目标状态时什么也不做，所以重复操作是安全的。租户不存在时返回 ErrNotFound。
func (a *SetDisabled) Handle(ctx context.Context, subject identity.Subject, id string) (*Tenant, error) {
	var t *Tenant
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if t, err = find(ctx, tx, id, true); err != nil {
			return err
		}
		if (t.DisabledAt != nil) == a.disabled {
			return nil
		}
		now := time.Now().UTC()
		t.DisabledAt, t.UpdatedAt = nil, now
		event := "tenant.enabled"
		if a.disabled {
			t.DisabledAt, event = &now, "tenant.disabled"
		}
		if _, err := tx.NewUpdate().Model(t).Column("disabled_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update tenant status")
		}
		return activity.Record(ctx, tx, subject, activity.Activity{Event: event, TargetType: "tenant", TargetID: t.ID})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// HTTP 处理 POST /tenants/{id}/disable 和 POST /tenants/{id}/enable。
func (a *SetDisabled) HTTP(ctx context.Context, in *IDInput) (*TenantOutput, error) {
	subject, _ := identity.FromContext(ctx)
	t, err := a.Handle(ctx, subject, in.ID)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.logins, a.addr, t)
	if err != nil {
		return nil, err
	}
	return &TenantOutput{Body: dto}, nil
}
