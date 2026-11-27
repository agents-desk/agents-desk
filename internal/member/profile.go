package member

import (
	"context"
	"strings"
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

// ProfileChanges 是成员修改个人资料的请求体：在名字与头像之外支持维护商务名片的头衔与简介。
type ProfileChanges struct {
	account.ProfileChanges
	Title *string `json:"title,omitempty" maxLength:"60" doc:"对外展示的头衔或职位，用于商务沟通名片"`
	Bio   *string `json:"bio,omitempty" maxLength:"240" doc:"对外展示的简短介绍，用于商务沟通名片"`
}

// ProfileInput 是成员修改个人资料的 HTTP 请求。
type ProfileInput struct {
	Body ProfileChanges
}

// UpdateProfile 修改成员自己的名字、头像及对外商务名片（头衔与简介）。
type UpdateProfile struct {
	db     *bun.DB
	logins *login.Realm
	files  *filesystem.Service
}

// NewUpdateProfile 返回修改个人资料的动作。
func NewUpdateProfile(db *bun.DB, logins *login.Realm, files *filesystem.Service) *UpdateProfile {
	return &UpdateProfile{db: db, logins: logins, files: files}
}

// Handle 在一个事务中修改 sub 的名字、头像和对外名片信息，并记录操作日志：换头像时确认新图片、删除原来的文件（见 account.ReplaceAvatar）。
// 返回修改后的个人资料。
func (a *UpdateProfile) Handle(ctx context.Context, sub identity.Subject, c ProfileChanges) (*Profile, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		row := new(Member)
		if err := tx.NewSelect().Model(row).Where("id = ?", sub.ID).For("UPDATE").Scan(ctx); err != nil {
			return errors.Wrap(err, "load members")
		}
		avatar, err := account.ReplaceAvatar(ctx, tx, a.files, sub, row.AvatarFileID, c.AvatarFileID)
		if err != nil {
			return err
		}
		nextTitle := row.Title
		if c.Title != nil {
			nextTitle = strings.TrimSpace(*c.Title)
		}
		nextBio := row.Bio
		if c.Bio != nil {
			nextBio = strings.TrimSpace(*c.Bio)
		}
		var changed []string
		if c.Name != row.Name {
			changed = append(changed, "name")
		}
		if avatar != row.AvatarFileID {
			changed = append(changed, "avatar")
		}
		if nextTitle != row.Title {
			changed = append(changed, "title")
		}
		if nextBio != row.Bio {
			changed = append(changed, "bio")
		}
		if len(changed) == 0 {
			return nil
		}
		row.Name, row.AvatarFileID, row.Title, row.Bio, row.UpdatedAt = c.Name, avatar, nextTitle, nextBio, time.Now().UTC()
		if _, err := tx.NewUpdate().Model(row).Column("name", "avatar_file_id", "title", "bio", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update members")
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
func (a *UpdateProfile) HTTP(ctx context.Context, in *ProfileInput) (*ProfileOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ProfileOutput{Body: *p}, nil
}
