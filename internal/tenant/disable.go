package tenant

import (
	"context"
	"log/slog"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// SetDisabled 停用或启用租户。停用后成员不能进入这个工作区，数据保留，可以随时启用；
// 停用提交后通过 creds 通知该工作区的在线长连接立即复查并断开。
type SetDisabled struct {
	db       *bun.DB
	logins   *login.Realm // 成员的 login.Realm，读取主管理员的邮箱
	addr     Addresses
	creds    identity.CredentialRechecker // 非 nil 时在停用提交后立即通知该租户的全部长连接复查
	disabled bool                         // true 是停用，false 是启用
}

// NewDisable 返回停用租户的动作。
func NewDisable(db *bun.DB, logins *login.Realm, addr Addresses, creds ...identity.CredentialRechecker) *SetDisabled {
	var c identity.CredentialRechecker
	if len(creds) > 0 {
		c = creds[0]
	}
	return &SetDisabled{db: db, logins: logins, addr: addr, creds: c, disabled: true}
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
		if a.disabled && a.creds != nil {
			tenantID := t.ID
			tx.AfterCommit(func(ctx context.Context) error {
				if err := a.creds.RecheckTenant(ctx, tenantID); err != nil {
					slog.WarnContext(ctx, "recheck disabled tenant failed", "tenant_id", tenantID, "error", err)
				}
				return nil
			})
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
