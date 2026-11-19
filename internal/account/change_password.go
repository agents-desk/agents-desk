package account

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// PasswordChange 是修改密码的请求。
type PasswordChange struct {
	CurrentPassword string `json:"current_password" minLength:"1" maxLength:"128"`
	NewPassword     string `json:"new_password" minLength:"1" maxLength:"128"`
}

// ChangePasswordInput 是修改密码的 HTTP 请求。
type ChangePasswordInput struct {
	Body PasswordChange
}

// ChangePassword 修改调用者自己的密码。平台管理员和成员各用自己的 login.Realm 和 apitoken.Store 构造一个。
type ChangePassword struct {
	db     *bun.DB
	logins *login.Realm
	tokens *apitoken.Store
}

// NewChangePassword 返回修改密码的动作。logins、tokens 是调用者这套账号的登录和登录 token。
func NewChangePassword(db *bun.DB, logins *login.Realm, tokens *apitoken.Store) *ChangePassword {
	return &ChangePassword{db: db, logins: logins, tokens: tokens}
}

// Handle 校验 sub 的当前密码，换成新密码并记录操作日志（不记录密码），再撤销 sub 的其他登录，本次登录保留。
// 当前密码不正确时返回 ErrWrongPassword（字段 current_password），失败次数与登录共用限制；
// 新密码不符合要求时返回 login.ErrWeakPassword（字段 new_password）。
func (a *ChangePassword) Handle(ctx context.Context, sub identity.Subject, p PasswordChange) error {
	if err := a.logins.Confirm(ctx, sub.Ref(), p.CurrentPassword); err != nil {
		if errors.Is(err, login.ErrInvalidCredentials) {
			return ErrWrongPassword.New(errorx.Field("current_password"))
		}
		return err
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := a.logins.SetPassword(ctx, tx, sub.Ref(), p.NewPassword); err != nil {
			if errors.Is(err, login.ErrWeakPassword) {
				return login.ErrWeakPassword.New(errorx.Field("new_password"))
			}
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: string(sub.Type) + ".password_changed", TargetType: string(sub.Type), TargetID: sub.ID,
		})
	})
	if err != nil {
		return err
	}
	_, err = a.tokens.RevokeOthers(ctx, sub)
	return err
}

// HTTP 处理 PUT /me/password。
func (a *ChangePassword) HTTP(ctx context.Context, in *ChangePasswordInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.Body)
}
