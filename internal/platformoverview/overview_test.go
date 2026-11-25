package platformoverview_test

import (
	"context"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/platformoverview"
	"agents-desk/internal/tenant"
)

func TestOverview(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	addr := tenant.Addresses{PublicURL: "https://desk.example"}

	if _, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, addr).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Acme", Slug: "acme", OwnerEmail: "owner@acme.example", OwnerName: "Owner", OwnerPassword: "password123",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := platformoverview.NewGet(env.DB, env.Files, addr).Handle(ctx, apptest.Admin, platformoverview.Input{TZ: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tenants == nil || got.Tenants.Total != 1 || got.Tenants.Active != 1 || got.Tenants.NewLast7Days != 1 {
		t.Fatalf("工作区统计不符：%+v", got.Tenants)
	}
	if got.Usage == nil || got.Usage.MembersTotal != 1 {
		t.Fatalf("平台用量统计不符：%+v", got.Usage)
	}
	if len(got.RecentTenants) != 1 || got.RecentTenants[0].Slug != "acme" {
		t.Fatalf("最近工作区不符：%+v", got.RecentTenants)
	}
	if got.Queue == nil || got.Admins == nil {
		t.Fatalf("队列或管理员统计不应为空：queue=%+v admins=%+v", got.Queue, got.Admins)
	}
	if len(got.RecentAudits) == 0 {
		t.Fatalf("应返回最近平台操作日志")
	}

	// 无业务权限的管理员只收到空概览，不报错
	restricted := identity.Subject{Type: platformadmin.Type, ID: "restricted"}
	limited, err := platformoverview.NewGet(env.DB, env.Files, addr).Handle(ctx, restricted, platformoverview.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if limited.Tenants != nil || limited.Admins != nil || limited.Usage != nil || limited.Queue != nil || len(limited.RecentAudits) != 0 {
		t.Fatalf("无权限管理员不应看到业务区块：%+v", limited)
	}
}
