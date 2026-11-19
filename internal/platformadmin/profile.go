package platformadmin

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/account"
)

// UpdateProfile 修改平台管理员自己的名字和头像。
type UpdateProfile struct {
	db     *bun.DB
	logins *login.Realm
	files  *filesystem.Service
}

// NewUpdateProfile 返回修改个人资料的动作。
func NewUpdateProfile(db *bun.DB, logins *login.Realm, files *filesystem.Service) *UpdateProfile {
	return &UpdateProfile{db: db, logins: logins, files: files}
}

// Handle 在一个事务中修改 sub 的名字和头像，并记录操作日志：换头像时确认新图片、删除原来的文件（见 account.ReplaceAvatar）。
// 返回修改后的个人资料。
func (a *UpdateProfile) Handle(ctx context.Context, sub identity.Subject, c account.ProfileChanges) (*Profile, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		row := new(Admin)
		if err := tx.NewSelect().Model(row).Where("id = ?", sub.ID).For("UPDATE").Scan(ctx); err != nil {
			return errors.Wrap(err, "load platform_admins")
		}
		avatar, err := account.ReplaceAvatar(ctx, tx, a.files, sub, row.AvatarFileID, c.AvatarFileID)
		if err != nil {
			return err
		}
		var changed []string
		if c.Name != row.Name {
			changed = append(changed, "name")
		}
		if avatar != row.AvatarFileID {
			changed = append(changed, "avatar")
		}
		if len(changed) == 0 {
			return nil
		}
		row.Name, row.AvatarFileID, row.UpdatedAt = c.Name, avatar, time.Now().UTC()
		if _, err := tx.NewUpdate().Model(row).Column("name", "avatar_file_id", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update platform_admins")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: string(Type) + ".profile_updated", TargetType: string(Type), TargetID: sub.ID,
			Properties: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	return loadProfile(ctx, a.db, a.logins, a.files, sub)
}

// HTTP 处理 PUT /me。
func (a *UpdateProfile) HTTP(ctx context.Context, in *account.ProfileInput) (*ProfileOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ProfileOutput{Body: *p}, nil
}
