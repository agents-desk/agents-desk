package member_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/login/logintest"
	"github.com/go-tenon/tenon/queue/queuetest"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/emails"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

func TestMemberPasswordResetAdminResetAndInviteMail(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	var sentCodes []login.Code
	logins := logintest.Realm(t, env.DB, login.Options{
		Type:    member.Type,
		Resolve: member.Resolve(env.DB, env.Roles),
		Policy:  login.Policy{PasswordKinds: []string{login.KindEmail}},
		Send:    func(_ context.Context, c login.Code) error { sentCodes = append(sentCodes, c); return nil },
	})
	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour})

	tnA, err := tenant.NewCreate(env.DB, env.Roles, logins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "alpha", OwnerEmail: "owner-a@example.com", OwnerName: "Owner A", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	tnB, err := tenant.NewCreate(env.DB, env.Roles, logins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Beta", Slug: "beta", OwnerEmail: "owner-b@example.com", OwnerName: "Owner B", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerA := identity.Subject{Type: member.Type, ID: tnA.OwnerID, Tenant: tnA.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	ownerB := identity.Subject{Type: member.Type, ID: tnB.OwnerID, Tenant: tnB.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", tnA.ID)
	if err != nil {
		t.Fatal(err)
	}
	agentRoleB, err := env.Roles.RoleByName(ctx, "坐席", tnB.ID)
	if err != nil {
		t.Fatal(err)
	}

	templates, err := emails.NewTemplates()
	if err != nil {
		t.Fatal(err)
	}
	addWithMail := member.NewAdd(env.DB, env.Roles, logins, env.Files, member.AddOptions{
		MailConfigured: func(context.Context) (bool, error) { return true, nil },
		Templates:      templates,
		PublicURL:      "https://desk.example.com",
	})

	// 1. 平台已配置发信时添加新邮箱成员可留空密码：自动生成初始密码并入队成员邀请邮件
	alice, err := addWithMail.Handle(ctx, ownerA, member.AddParams{
		Email:  "alice@example.com",
		Name:   "Alice",
		RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatalf("邮件已配置时免密创建新成员失败：%v", err)
	}
	pending := queuetest.Pending(t, env.DB, emails.SendTopic)
	if len(pending) != 1 || len(pending[0].To) == 0 || pending[0].To[0].Email != "alice@example.com" {
		t.Fatalf("应入队 1 封新成员邀请邮件，实际 %+v", pending)
	}

	// 将已有账号 Alice 加入第二个工作区同样发送加入通知邮件
	if _, err := addWithMail.Handle(ctx, ownerB, member.AddParams{
		Email:  "alice@example.com",
		RoleID: agentRoleB.ID,
	}); err != nil {
		t.Fatalf("已有成员加入第二工作区失败：%v", err)
	}
	if pending = queuetest.Pending(t, env.DB, emails.SendTopic); len(pending) != 2 {
		t.Fatalf("加入第二工作区应再入队 1 封邀请邮件，实际 %d", len(pending))
	}

	// 2. 成员忘记密码自助重置：旧 token 全部失效、新密码可登录
	tok1, err := tokens.Login(ctx, identity.Subject{Type: member.Type, ID: alice.ID}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	pr := member.NewPasswordReset(logins, tokens)
	req, err := pr.Forgot(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(sentCodes) != 1 || sentCodes[0].Code == "" {
		t.Fatalf("忘记密码应触发 SendCode 回调，实际 %+v", sentCodes)
	}
	if err := pr.Reset(ctx, req.RequestID, sentCodes[0].Code, "alice-reset-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Validate(ctx, tok1); err == nil {
		t.Fatal("成员重设密码后旧令牌应已失效")
	}
	if _, err := logins.Password(ctx, login.Email("alice@example.com"), "alice-reset-123"); err != nil {
		t.Fatalf("新密码应可登录：%v", err)
	}

	// 3. 工作区管理员重置下属成员密码（直接设密、邮件发码）与保护校验
	tok2, err := tokens.Login(ctx, identity.Subject{Type: member.Type, ID: alice.ID}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	adminReset := member.NewResetMemberPassword(env.DB, env.Roles, logins, tokens)
	mode, _, err := adminReset.Handle(ctx, ownerA, alice.ID, member.ResetPasswordBody{Password: "admin-set-456"})
	if err != nil || mode != "password" {
		t.Fatalf("管理员直接重置密码失败：mode=%q err=%v", mode, err)
	}
	if _, err := tokens.Validate(ctx, tok2); err == nil {
		t.Fatal("管理员重置密码后成员旧令牌应已失效")
	}
	if _, err := logins.Password(ctx, login.Email("alice@example.com"), "admin-set-456"); err != nil {
		t.Fatalf("管理员直接设密后应可登录：%v", err)
	}

	mode, toEmail, err := adminReset.Handle(ctx, ownerA, alice.ID, member.ResetPasswordBody{SendResetEmail: true})
	if err != nil || mode != "email" || toEmail != "alice@example.com" || len(sentCodes) != 2 {
		t.Fatalf("管理员触发邮件重置异常：mode=%q email=%q codes=%d err=%v", mode, toEmail, len(sentCodes), err)
	}

	// 保护规则：不能操作自己、不能操作主管理员、不能跨工作区操作其他工作区成员
	if _, _, err := adminReset.Handle(ctx, ownerA, tnA.OwnerID, member.ResetPasswordBody{Password: "x1234567"}); !errors.Is(err, member.ErrSelf) {
		t.Fatalf("操作自己应返回 ErrSelf，实际 %v", err)
	}
	adminRoleA, err := env.Roles.RoleByName(ctx, "管理员", tnA.ID)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := member.NewAdd(env.DB, env.Roles, logins, env.Files).Handle(ctx, ownerA, member.AddParams{
		Email: "mgr-a@example.com", Name: "Mgr", Password: "password123", RoleID: adminRoleA.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgrSub := identity.Subject{Type: member.Type, ID: mgr.ID, Tenant: tnA.ID, Permissions: auth.WithBase(adminRoleA.Permissions)}
	if _, _, err := adminReset.Handle(ctx, mgrSub, tnA.OwnerID, member.ResetPasswordBody{Password: "x1234567"}); !errors.Is(err, member.ErrOwnerProtected) {
		t.Fatalf("操作主管理员应返回 ErrOwnerProtected，实际 %v", err)
	}
	if _, _, err := adminReset.Handle(ctx, ownerA, tnB.OwnerID, member.ResetPasswordBody{Password: "x1234567"}); !errors.Is(err, member.ErrNotFound) {
		t.Fatalf("跨工作区重置他人成员密码应返回 ErrNotFound，实际 %v", err)
	}
}
