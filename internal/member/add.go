package member

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/role"
)

// AddParams 是添加成员的请求。邮箱已有账号时（例如已在别的工作区）直接加入，名字和初始密码不使用。
type AddParams struct {
	Email    string `json:"email" format:"email" maxLength:"254"`
	Name     string `json:"name,omitempty" maxLength:"60" doc:"这个邮箱还没有账号时必填"`
	Password string `json:"password,omitempty" maxLength:"128" doc:"新账号的初始密码，由你告诉本人；这个邮箱还没有账号时必填"`
	RoleID   string `json:"role_id" format:"uuid" doc:"角色：这个工作区自己的角色"`
}

// AddInput 是添加成员的 HTTP 请求。
type AddInput struct {
	Body AddParams
}

// Add 把一个人加入当前工作区：邮箱没有账号时同时创建账号。
type Add struct {
	db     *bun.DB
	logins *login.Realm
	files  *filesystem.Service
}

// NewAdd 返回添加成员的动作。
func NewAdd(db *bun.DB, logins *login.Realm, files *filesystem.Service) *Add {
	return &Add{db: db, logins: logins, files: files}
}

// Handle 在一个事务中找到或创建邮箱为 p.Email 的账号，在 sub 所在的工作区中分配角色 p.RoleID，并记录操作日志。
// 他已经是这个工作区的成员时返回 ErrAlreadyMember；角色不是这个工作区自己的角色时返回 role.ErrNotFound；
// 账号的错误见 Ensure。错误都指向对应的字段。
func (a *Add) Handle(ctx context.Context, sub identity.Subject, p AddParams) (*TenantMember, error) {
	var added *Member
	var assigned authz.Role
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		m, err := Ensure(ctx, tx, a.logins, Account{Email: p.Email, Name: p.Name, Password: p.Password})
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
		roles := authz.NewStore(tx, auth.Catalog)
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
		if err := roles.Assign(ctx, m.Ref(), sub.Tenant, assigned.ID); err != nil {
			if errors.Is(err, authz.ErrRoleNotFound) {
				return role.ErrNotFound.New(errorx.Field("role_id"))
			}
			return err
		}
		added = m
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
