package platformadmin_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/platformadmin"
)

var system = identity.Subject{Type: identity.System, ID: "test"}

func TestCreateGrantsPlatformAdminRole(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	admin, err := platformadmin.NewCreate(env.DB, env.AdminLogins).Handle(ctx, system, "运维", "ops@example.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	out, err := env.AdminLogins.Password(ctx, login.Email("ops@example.com"), "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	if out.Subject.ID != admin.ID {
		t.Fatalf("登录得到 %s，期望 %s", out.Subject.ID, admin.ID)
	}
	if !out.Subject.Can(string(auth.TenantsManage)) || !out.Subject.Can(string(auth.PlatformSettingsManage)) {
		t.Fatalf("权限 %v 中缺少平台管理员的权限", out.Subject.Permissions)
	}
	var events int
	if err := env.DB.NewRaw("SELECT count(*) FROM activity_log WHERE event = 'platform_admin.created' AND target_id = ?", admin.ID).Scan(ctx, &events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("操作日志有 %d 条，期望 1 条", events)
	}
}

func TestCreateRejectsTakenEmailAndWeakPassword(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	create := platformadmin.NewCreate(env.DB, env.AdminLogins)
	if _, err := create.Handle(ctx, system, "运维", "ops@example.com", "secret-pass-123"); err != nil {
		t.Fatal(err)
	}

	if _, err := create.Handle(ctx, system, "另一个", "OPS@example.com", "secret-pass-456"); !errors.Is(err, platformadmin.ErrEmailTaken) {
		t.Fatalf("邮箱重复：得到 %v", err)
	}
	if _, err := create.Handle(ctx, system, "弱密码", "weak@example.com", "short"); !errors.Is(err, login.ErrWeakPassword) {
		t.Fatalf("弱密码：得到 %v", err)
	}
	// 两次失败都整体回滚，表中只有第一个管理员
	var n int
	if err := env.DB.NewRaw("SELECT count(*) FROM platform_admins").Scan(ctx, &n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("platform_admins 有 %d 行，期望 1 行", n)
	}
}

func TestCreateCommandReadsPasswordFromStdin(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)

	cmd := platformadmin.Commands(env.DB, env.AdminLogins)
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader("secret-pass-123\n"))
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"create", "--email", "ops@example.com"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "已创建平台管理员 ops") {
		t.Fatalf("输出 %q", out.String())
	}
	if _, err := env.AdminLogins.Password(context.Background(), login.Email("ops@example.com"), "secret-pass-123"); err != nil {
		t.Fatal(err)
	}
}
