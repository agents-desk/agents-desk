package platformadmin

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// SetDisabled 停用或启用平台管理员账号。停用时同时撤销该管理员的全部登录令牌，不能停用自己。
type SetDisabled struct {
	db       *bun.DB
	logins   *login.Realm
	disabled bool // true 是停用，false 是启用
}

// NewDisable 返回停用平台管理员的动作。
func NewDisable(db *bun.DB, logins *login.Realm) *SetDisabled {
	return &SetDisabled{db: db, logins: logins, disabled: true}
}

// NewEnable 返回启用平台管理员的动作。
func NewEnable(db *bun.DB, logins *login.Realm) *SetDisabled {
	return &SetDisabled{db: db, logins: logins, disabled: false}
}

// Handle 停用或启用平台管理员 id 并记录操作日志。停用自己返回 ErrSelf，不存在返回 ErrNotFound。
func (a *SetDisabled) Handle(ctx context.Context, subject identity.Subject, id string) (*Admin, error) {
	if a.disabled && subject.Type == Type && subject.ID == id {
		return nil, ErrSelf.New()
	}
	var admin *Admin
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if admin, err = findLocked(ctx, tx, id, true); err != nil {
			return err
		}
		if (admin.DisabledAt != nil) == a.disabled {
			return nil
		}
		now := time.Now().UTC()
		admin.DisabledAt, admin.UpdatedAt = nil, now
		event := "platform_admin.enabled"
		if a.disabled {
			admin.DisabledAt, event = &now, "platform_admin.disabled"
		}
		if _, err := tx.NewUpdate().Model(admin).Column("disabled_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update platform admin status")
		}
		if a.disabled {
			if err := apitoken.Erase(ctx, tx, admin.Ref()); err != nil {
				return err
			}
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: event, TargetType: string(Type), TargetID: admin.ID,
			Properties: map[string]any{"name": admin.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	return admin, nil
}

// HTTP 处理 POST /admins/{id}/disable 和 POST /admins/{id}/enable。
func (a *SetDisabled) HTTP(ctx context.Context, in *IDInput) (*AdminOutput, error) {
	sub, _ := identity.FromContext(ctx)
	admin, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.logins, admin, sub.ID)
	if err != nil {
		return nil, err
	}
	return &AdminOutput{Body: dto}, nil
}
