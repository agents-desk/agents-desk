package member

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
)

// ErrResetPasswordParams 表示既未填写新密码也未选择发送重置邮件，或者成员没有绑定登录邮箱。
var ErrResetPasswordParams = errorx.Define(errorx.InvalidArgument, "member.reset_password_invalid")

// ResetPasswordBody 是工作区管理员重置成员密码的请求体。
type ResetPasswordBody struct {
	Password       string `json:"password,omitempty" maxLength:"128" doc:"直接为成员设置的新密码；与 send_reset_email 二选一"`
	SendResetEmail bool   `json:"send_reset_email,omitempty" doc:"为 true 时发送重置密码验证码邮件给成员并强制下线"`
}

// ResetMemberPasswordInput 是重置成员密码的 HTTP 请求。
type ResetMemberPasswordInput struct {
	ID   string `path:"id" format:"uuid"`
	Body ResetPasswordBody
}

// ResetPasswordOutput 是重置成员密码的响应，返回实际执行的方式。
type ResetPasswordOutput struct {
	Body struct {
		Mode  string `json:"mode" enum:"password,email"`
		Email string `json:"email,omitempty"`
	}
}

// ResetMemberPassword 由工作区管理员重置下属成员的密码或发送密码重置邮件，并撤销目标成员全部现有登录令牌。
type ResetMemberPassword struct {
	db     *bun.DB
	roles  *authz.Store
	logins *login.Realm
	tokens *apitoken.Store
}

// NewResetMemberPassword 返回工作区管理员重置成员密码的动作。
func NewResetMemberPassword(db *bun.DB, roles *authz.Store, logins *login.Realm, tokens *apitoken.Store) *ResetMemberPassword {
	return &ResetMemberPassword{db: db, roles: roles, logins: logins, tokens: tokens}
}

// Handle 校验调用者权限并重置成员 id 的密码或发送重置邮件，完成后撤销该成员全部登录令牌。
// 不能操作本人（ErrSelf）、不能操作主管理员（ErrOwnerProtected）、不能越权操作高权限成员（authz.ErrExceedsPermissions）。
func (a *ResetMemberPassword) Handle(ctx context.Context, sub identity.Subject, id string, body ResetPasswordBody) (string, string, error) {
	if id == sub.ID {
		return "", "", ErrSelf.New()
	}
	password := strings.TrimSpace(body.Password)
	if password == "" && !body.SendResetEmail {
		return "", "", ErrResetPasswordParams.New(errorx.Field("password"))
	}
	if password != "" && body.SendResetEmail {
		return "", "", ErrResetPasswordParams.New(errorx.Field("send_reset_email"))
	}

	ref := identity.Ref{Type: Type, ID: id}
	var email string
	mode := "password"
	if body.SendResetEmail {
		mode = "email"
	}

	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		roles := a.roles.WithDB(tx)
		current, err := RoleOf(ctx, roles, ref, sub.Tenant)
		if err != nil {
			return err
		}
		if auth.IsOwner(current) {
			return ErrOwnerProtected.New()
		}
		if err := roles.CheckGrant(sub, current.Permissions); err != nil {
			return err
		}
		if body.SendResetEmail {
			email, err = Email(ctx, a.logins.WithDB(tx), ref)
			if err != nil {
				return err
			}
			if email == "" {
				return ErrResetPasswordParams.New(errorx.Field("send_reset_email"))
			}
		} else {
			if err := a.logins.SetPassword(ctx, tx, ref, body.Password); err != nil {
				if errors.Is(err, login.ErrWeakPassword) {
					return login.ErrWeakPassword.New(errorx.Field("password"))
				}
				return err
			}
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.password_reset", TargetType: string(Type), TargetID: id,
			Properties: map[string]any{"mode": mode},
		})
	})
	if err != nil {
		return "", "", err
	}
	if a.tokens != nil {
		if _, err := a.tokens.RevokeSubject(ctx, ref); err != nil {
			return "", "", err
		}
	}
	if body.SendResetEmail {
		if _, err := a.logins.SendCode(ctx, login.Reset, login.Email(email)); err != nil {
			return "", "", err
		}
	}
	return mode, email, nil
}

// HTTP 处理 POST /t/{slug}/api/members/{id}/reset-password。
func (a *ResetMemberPassword) HTTP(ctx context.Context, in *ResetMemberPasswordInput) (*ResetPasswordOutput, error) {
	sub, _ := identity.FromContext(ctx)
	mode, email, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	out := &ResetPasswordOutput{}
	out.Body.Mode = mode
	out.Body.Email = email
	return out, nil
}
