package platformadmin

import (
	"context"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// UpdateInput 是修改平台管理员的请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"60"`
	}
}

// Update 修改平台管理员的名字。
type Update struct {
	db     *bun.DB
	logins *login.Realm
}

// NewUpdate 返回修改平台管理员的动作。
func NewUpdate(db *bun.DB, logins *login.Realm) *Update { return &Update{db: db, logins: logins} }

// Handle 修改平台管理员 id 的名字并记录操作日志。不存在时返回 ErrNotFound。
func (a *Update) Handle(ctx context.Context, subject identity.Subject, id, name string) (*Admin, error) {
	name = strings.TrimSpace(name)
	var admin *Admin
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if admin, err = findLocked(ctx, tx, id, true); err != nil {
			return err
		}
		if admin.Name == name {
			return nil
		}
		old := admin.Name
		admin.Name, admin.UpdatedAt = name, time.Now().UTC()
		if _, err := tx.NewUpdate().Model(admin).Column("name", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update platform admin")
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "platform_admin.updated", TargetType: string(Type), TargetID: admin.ID,
			Properties: map[string]any{"from": old, "to": admin.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	return admin, nil
}

// HTTP 处理 PUT /admins/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*AdminOutput, error) {
	sub, _ := identity.FromContext(ctx)
	admin, err := a.Handle(ctx, sub, in.ID, in.Body.Name)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.logins, admin, sub.ID)
	if err != nil {
		return nil, err
	}
	return &AdminOutput{Body: dto}, nil
}
