package member

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/mail"

	"agents-desk/internal/emails"
	"agents-desk/internal/lang"
	"agents-desk/internal/role"
)

// AddOptions 配置添加成员时的邀请邮件发送能力。
type AddOptions struct {
	MailConfigured func(context.Context) (bool, error) // 报告平台邮件服务是否可用
	Templates      *mail.Templates                     // 解析后的邮件模板
	PublicURL      string                              // 应用对外根地址，用于拼接工作区登录链接
}

// AddParams 是添加成员的请求。邮箱已有账号时（例如已在别的工作区）直接加入，名字和语言参数不使用；
// 平台已配置邮件时密码可留空，系统将生成随机初始密码并发送加入邀请邮件。
type AddParams struct {
	Email    string `json:"email" format:"email" maxLength:"254"`
	Name     string `json:"name,omitempty" maxLength:"60" doc:"这个邮箱还没有账号时必填"`
	Password string `json:"password,omitempty" maxLength:"128" doc:"新账号的初始密码；平台已配置邮件服务时可留空并由成员通过邮件重设密码"`
	Language string `json:"language,omitempty" maxLength:"35" doc:"可选：新账号的沟通与译文偏好语言（BCP 47，如 zh-CN、en、hi-Latn、pt-BR）；为空默认 zh-CN"`
	RoleID   string `json:"role_id" format:"uuid" doc:"角色：这个工作区自己的角色"`
}

// AddInput 是添加成员的 HTTP 请求。
type AddInput struct {
	Body AddParams
}

// Add 把一个人加入当前工作区：邮箱没有账号时同时创建账号，并在配置了邮件服务时异步发送工作区邀请邮件。
type Add struct {
	db     *bun.DB
	roles  *authz.Store
	logins *login.Realm
	files  *filesystem.Service
	opts   AddOptions
}

// NewAdd 返回添加成员的动作。
func NewAdd(db *bun.DB, roles *authz.Store, logins *login.Realm, files *filesystem.Service, opts ...AddOptions) *Add {
	var o AddOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return &Add{db: db, roles: roles, logins: logins, files: files, opts: o}
}

// Handle 在一个事务中找到或创建邮箱为 p.Email 的账号，在 sub 所在的工作区中分配角色 p.RoleID，并记录操作日志。
// 他已经是这个工作区的成员时返回 ErrAlreadyMember；角色不是这个工作区自己的角色时返回 role.ErrNotFound；
// 角色权限超出 sub 自身权限时返回 authz.ErrExceedsPermissions；账号的错误见 Ensure。错误都指向对应的字段。
func (a *Add) Handle(ctx context.Context, sub identity.Subject, p AddParams) (*TenantMember, error) {
	mailReady := false
	if a.opts.MailConfigured != nil && a.opts.Templates != nil {
		ok, err := a.opts.MailConfigured(ctx)
		if err != nil {
			return nil, err
		}
		mailReady = ok
	}
	password := p.Password
	generatedPassword := false
	if strings.TrimSpace(password) == "" && mailReady {
		if _, err := a.logins.Lookup(ctx, login.Email(p.Email)); errors.Is(err, login.ErrNotFound) {
			buf := make([]byte, 18)
			if _, rerr := rand.Read(buf); rerr != nil {
				return nil, errors.Wrap(rerr, "generate initial password")
			}
			password = base64.RawURLEncoding.EncodeToString(buf)
			generatedPassword = true
		} else if err != nil {
			return nil, err
		}
	}

	var added *Member
	var assigned authz.Role
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		m, err := Ensure(ctx, tx, a.logins, Account{Email: p.Email, Name: p.Name, Password: password, Language: p.Language})
		switch {
		case errors.Is(err, ErrDisabled):
			return ErrDisabled.New(errorx.Field("email"))
		case errors.Is(err, ErrDetailsMissing) && p.Name == "":
			return ErrDetailsMissing.New(errorx.Field("name"))
		case errors.Is(err, ErrDetailsMissing):
			return ErrDetailsMissing.New(errorx.Field("password"))
		case errors.Is(err, login.ErrWeakPassword):
			return login.ErrWeakPassword.New(errorx.Field("password"))
		case err != nil:
			return err
		}
		roles := a.roles.WithDB(tx)
		existing, err := roles.RolesOf(ctx, m.Ref(), sub.Tenant)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return ErrAlreadyMember.New(errorx.Field("email"))
		}
		if assigned, err = role.Find(ctx, roles, sub.Tenant, p.RoleID, errorx.Field("role_id")); err != nil {
			return err
		}
		if err := roles.CheckGrant(sub, assigned.Permissions); err != nil {
			return err
		}
		if err := roles.SetRoles(ctx, m.Ref(), sub.Tenant, assigned.ID); err != nil {
			if errors.Is(err, authz.ErrRoleNotFound) {
				return role.ErrNotFound.New(errorx.Field("role_id"))
			}
			return err
		}
		added = m
		if mailReady {
			var tRow struct {
				Name string `bun:"name"`
				Slug string `bun:"slug"`
			}
			if err := tx.NewSelect().Table("tenants").Column("name", "slug").Where("id = ?", sub.Tenant).Scan(ctx, &tRow); err != nil {
				return errors.Wrap(err, "load tenant for invite email")
			}
			locale := string(m.UILanguage)
			if locale == "" {
				locale = lang.MatchUI(string(m.Language))
			}
			renderCtx := emails.WithLocale(ctx, locale)
			loginURL := strings.TrimRight(strings.TrimSpace(a.opts.PublicURL), "/") + "/app/" + tRow.Slug + "/"
			if strings.TrimSpace(a.opts.PublicURL) == "" {
				loginURL = "/app/" + tRow.Slug + "/"
			}
			msg, err := a.opts.Templates.Render(renderCtx, "member_invite", map[string]any{
				"TenantName":        tRow.Name,
				"RoleName":          assigned.Name,
				"MemberName":        m.Name,
				"LoginURL":          loginURL,
				"GeneratedPassword": generatedPassword,
			})
			if err != nil {
				return errors.Wrap(err, "render member invite email")
			}
			msg.To = []mail.Address{{Name: m.Name, Email: strings.TrimSpace(p.Email)}}
			if err := emails.SendTopic.Publish(ctx, tx, msg); err != nil {
				return errors.Wrap(err, "queue member invite email")
			}
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.added", TargetType: string(Type), TargetID: m.ID,
			Properties: map[string]any{"role_id": assigned.ID, "role": assigned.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	item, err := toTenantMember(ctx, a.logins, a.files, sub, added, assigned, Away)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// HTTP 处理 POST /t/{slug}/api/members。
func (a *Add) HTTP(ctx context.Context, in *AddInput) (*TenantMemberOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TenantMemberOutput{Body: *item}, nil
}
