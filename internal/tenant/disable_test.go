package tenant_test

import (
	"context"
	"testing"

	"agents-desk/internal/apptest"
	"agents-desk/internal/tenant"
)

func TestDisableAndEnableAreIdempotent(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	ten, err := tenant.NewCreate(env.DB, env.MemberLogins, addr).Handle(ctx, apptest.Admin, acme)
	if err != nil {
		t.Fatal(err)
	}
	disable, enable := tenant.NewDisable(env.DB, env.MemberLogins, addr), tenant.NewEnable(env.DB, env.MemberLogins, addr)

	for range 2 {
		got, err := disable.Handle(ctx, apptest.Admin, ten.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status() != tenant.Disabled {
			t.Fatalf("停用后的状态：%s", got.Status())
		}
	}
	got, err := enable.Handle(ctx, apptest.Admin, ten.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status() != tenant.Active {
		t.Fatalf("启用后的状态：%s", got.Status())
	}
	// 重复停用只记录一次
	if n := count(t, env, "SELECT count(*) FROM activity_log WHERE event IN ('tenant.disabled', 'tenant.enabled')"); n != 2 {
		t.Fatalf("操作日志有 %d 条，期望 2 条", n)
	}
}
