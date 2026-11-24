package tenant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

func TestChangeOwnerMovesOwnerRole(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	ten, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, addr).Handle(ctx, apptest.Admin, acme)
	if err != nil {
		t.Fatal(err)
	}
	alice := identity.Ref{Type: member.Type, ID: ten.OwnerID}
	change := tenant.NewChangeOwner(env.DB, env.Roles, env.MemberLogins, addr, member.Hooks{Removed: []member.RemovedHook{team.OnMemberRemoved}})

	// Alice 在线，并且在一个团队收件箱中
	inTenant := identity.Subject{Type: identity.System, ID: "test", Tenant: ten.ID}
	tm, err := team.NewCreate(env.DB).Handle(ctx, inTenant, team.Changes{Name: "一线"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := team.NewPutMember(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, inTenant, tm.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	asAlice := identity.Subject{Type: member.Type, ID: alice.ID, Tenant: ten.ID}
	if _, err := member.NewSetMyStatus(env.DB).Handle(ctx, asAlice, member.StatusSetting{Status: member.Online}); err != nil {
		t.Fatal(err)
	}

	changed, err := change.Handle(ctx, apptest.Admin, ten.ID, tenant.OwnerParams{Email: "bob@acme.com", Name: "Bob", Password: "bob-pass-1234"})
	if err != nil {
		t.Fatal(err)
	}
	bob := identity.Ref{Type: member.Type, ID: changed.OwnerID}
	if changed.OwnerID == alice.ID || changed.Owner.Name != "Bob" {
		t.Fatalf("主管理员没有换成 Bob：%+v", changed.Owner)
	}
	// 原主管理员离开这个租户：没有角色，在线状态和团队收件箱中的记录都删除了
	if got := rolesOf(t, env, alice, ten.ID); len(got) != 0 {
		t.Fatalf("Alice 仍有角色：%v", got)
	}
	if n := count(t, env, "SELECT count(*) FROM member_statuses WHERE member_id = ?", alice.ID); n != 0 {
		t.Errorf("Alice 的在线状态还在")
	}
	if n := count(t, env, "SELECT count(*) FROM team_members WHERE member_id = ?", alice.ID); n != 0 {
		t.Errorf("Alice 还在团队收件箱中")
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

	// 已经是成员的人成为主管理员：撤销他原来的角色，每人只有一个角色
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", ten.ID)
	if err != nil {
		t.Fatal(err)
	}
	carol, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, inTenant,
		member.AddParams{Email: "carol@acme.com", Name: "Carol", Password: "carol-pass-1234", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := change.Handle(ctx, apptest.Admin, ten.ID, tenant.OwnerParams{Email: "carol@acme.com"}); err != nil {
		t.Fatal(err)
	}
	if got := rolesOf(t, env, identity.Ref{Type: member.Type, ID: carol.ID}, ten.ID); len(got) != 1 || got[0] != "owner" {
		t.Fatalf("Carol 的角色：%v", got)
	}
}

func TestChangeOwnerOfMissingTenant(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	_, err := tenant.NewChangeOwner(env.DB, env.Roles, env.MemberLogins, addr, member.Hooks{}).Handle(context.Background(), apptest.Admin,
		"01900000-0000-7000-8000-0000000000ff", tenant.OwnerParams{Email: "bob@acme.com", Name: "Bob", Password: "bob-pass-1234"})
	if !errors.Is(err, tenant.ErrNotFound) {
		t.Fatalf("得到 %v", err)
	}
}
