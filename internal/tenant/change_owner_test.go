package tenant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

func TestChangeOwnerMovesOwnerRole(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	ten, err := tenant.NewCreate(env.DB, env.MemberLogins, addr).Handle(ctx, apptest.Admin, acme)
	if err != nil {
		t.Fatal(err)
	}
	alice := identity.Ref{Type: member.Type, ID: ten.OwnerID}
	change := tenant.NewChangeOwner(env.DB, env.MemberLogins, addr)

	changed, err := change.Handle(ctx, apptest.Admin, ten.ID, tenant.OwnerParams{Email: "bob@acme.com", Name: "Bob", Password: "bob-pass-1234"})
	if err != nil {
		t.Fatal(err)
	}
	bob := identity.Ref{Type: member.Type, ID: changed.OwnerID}
	if changed.OwnerID == alice.ID || changed.Owner.Name != "Bob" {
		t.Fatalf("主管理员没有换成 Bob：%+v", changed.Owner)
	}
	// 原主管理员没有其他角色，撤销 owner 后就不再是这个租户的成员
	if got := rolesOf(t, env, alice, ten.ID); len(got) != 0 {
		t.Fatalf("Alice 仍有角色：%v", got)
	}
	if got := rolesOf(t, env, bob, ten.ID); len(got) != 1 {
		t.Fatalf("Bob 的角色：%v", got)
	}

	// 换成现在的主管理员时什么也不做
	if _, err := change.Handle(ctx, apptest.Admin, ten.ID, tenant.OwnerParams{Email: "bob@acme.com"}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, env, "SELECT count(*) FROM activity_log WHERE event = 'tenant.owner_changed'"); n != 1 {
		t.Fatalf("操作日志有 %d 条，期望 1 条", n)
	}
}

func TestChangeOwnerOfMissingTenant(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	_, err := tenant.NewChangeOwner(env.DB, env.MemberLogins, addr).Handle(context.Background(), apptest.Admin,
		"01900000-0000-7000-8000-0000000000ff", tenant.OwnerParams{Email: "bob@acme.com", Name: "Bob", Password: "bob-pass-1234"})
	if !errors.Is(err, tenant.ErrNotFound) {
		t.Fatalf("得到 %v", err)
	}
}
