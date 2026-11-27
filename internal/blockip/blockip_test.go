package blockip_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/blockip"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestBlockIPCRUDMatchAndIsolation 验证 IP/CIDR 规范化、去重校验、网段命中、删除及跨工作区隔离。
func TestBlockIPCRUDMatchAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "ip-alpha", OwnerEmail: "owner@ip-alpha.test", OwnerName: "Alpha", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Beta", Slug: "ip-beta", OwnerEmail: "owner@ip-beta.test", OwnerName: "Beta", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	subA := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID}
	subB := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID}

	create := blockip.NewCreate(env.DB)
	list := blockip.NewList(env.DB)
	del := blockip.NewDelete(env.DB)

	if _, err := create.Handle(ctx, subA, blockip.CreateBody{CIDR: ""}); !errors.Is(err, blockip.ErrInvalidCIDR) {
		t.Fatalf("空输入应返回 ErrInvalidCIDR，实际 %v", err)
	}
	if _, err := create.Handle(ctx, subA, blockip.CreateBody{CIDR: "999.999.999.999"}); !errors.Is(err, blockip.ErrInvalidCIDR) {
		t.Fatalf("非法 IP 应返回 ErrInvalidCIDR，实际 %v", err)
	}

	single, err := create.Handle(ctx, subA, blockip.CreateBody{CIDR: " 1.2.3.4 ", Reason: "恶意刷单"})
	if err != nil {
		t.Fatal(err)
	}
	if single.CIDR != "1.2.3.4/32" || single.Reason != "恶意刷单" {
		t.Fatalf("单 IP 规范化不符：%+v", single)
	}

	subnet, err := create.Handle(ctx, subA, blockip.CreateBody{CIDR: "10.20.30.77/24", Reason: "机房网段"})
	if err != nil {
		t.Fatal(err)
	}
	if subnet.CIDR != "10.20.30.0/24" {
		t.Fatalf("CIDR 掩码归零不符：%+v", subnet)
	}

	v6, err := create.Handle(ctx, subA, blockip.CreateBody{CIDR: "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	if v6.CIDR != "2001:db8::1/128" {
		t.Fatalf("IPv6 规范化不符：%+v", v6)
	}

	if _, err := create.Handle(ctx, subA, blockip.CreateBody{CIDR: "1.2.3.4/32"}); !errors.Is(err, blockip.ErrDuplicate) {
		t.Fatalf("重复规则应返回 ErrDuplicate，实际 %v", err)
	}

	for _, tc := range []struct {
		tenant string
		ip     string
		want   bool
	}{
		{alpha.ID, "1.2.3.4", true},
		{alpha.ID, "::ffff:1.2.3.4", true},
		{alpha.ID, "10.20.30.250", true},
		{alpha.ID, "2001:db8::1", true},
		{alpha.ID, "1.2.3.5", false},
		{alpha.ID, "not-an-ip", false},
		{beta.ID, "1.2.3.4", false},
	} {
		got, err := blockip.IsBlocked(ctx, env.DB, tc.tenant, tc.ip)
		if err != nil {
			t.Fatalf("IsBlocked(%s, %s) err=%v", tc.tenant, tc.ip, err)
		}
		if got != tc.want {
			t.Fatalf("IsBlocked(%s, %s) = %v, want %v", tc.tenant, tc.ip, got, tc.want)
		}
	}

	listA, err := list.Handle(ctx, subA)
	if err != nil || len(listA) != 3 {
		t.Fatalf("Alpha 列表异常：len=%d err=%v", len(listA), err)
	}
	listB, err := list.Handle(ctx, subB)
	if err != nil || len(listB) != 0 {
		t.Fatalf("Beta 应看不到 Alpha 规则：len=%d err=%v", len(listB), err)
	}

	if err := del.Handle(ctx, subB, single.ID); !errors.Is(err, blockip.ErrNotFound) {
		t.Fatalf("跨工作区删除应返回 ErrNotFound，实际 %v", err)
	}
	if err := del.Handle(ctx, subA, single.ID); err != nil {
		t.Fatal(err)
	}
	if err := del.Handle(ctx, subA, single.ID); !errors.Is(err, blockip.ErrNotFound) {
		t.Fatalf("重复删除应返回 ErrNotFound，实际 %v", err)
	}
	blocked, err := blockip.IsBlocked(ctx, env.DB, alpha.ID, "1.2.3.4")
	if err != nil || blocked {
		t.Fatalf("移除规则后应放行：blocked=%v err=%v", blocked, err)
	}
}
