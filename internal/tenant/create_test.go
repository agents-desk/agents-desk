package tenant_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

var addr = tenant.Addresses{PublicURL: "https://desk.example.com"}

// acme 是新建 acme 的参数，所有者 alice 还没有账号。
var acme = tenant.CreateParams{Name: "Acme", Slug: "acme", OwnerEmail: "alice@acme.com", OwnerName: "Alice", OwnerPassword: "alice-pass-123"}

// rolesOf 返回 ref 在租户 id 中的角色 ID。
func rolesOf(t *testing.T, env *apptest.Env, ref identity.Ref, id string) []string {
	t.Helper()
	roles, err := env.Roles.RolesOf(context.Background(), ref, id)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(roles))
	for i, r := range roles {
		ids[i] = r.ID
	}
	return ids
}

// count 返回 SQL 查询的计数。
func count(t *testing.T, env *apptest.Env, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.DB.NewRaw(query, args...).Scan(context.Background(), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateMakesOwnerAccountAndRole(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	ten, err := tenant.NewCreate(env.DB, env.MemberLogins, addr).Handle(ctx, apptest.Admin, acme)
	if err != nil {
		t.Fatal(err)
	}
	// 所有者的账号能用初始密码登录，并且在这个租户中是 owner
	out, err := env.MemberLogins.Password(ctx, login.Email("alice@acme.com"), "alice-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	if out.Subject.ID != ten.OwnerID {
		t.Fatalf("所有者是 %s，登录得到 %s", ten.OwnerID, out.Subject.ID)
	}
	if got := rolesOf(t, env, out.Subject.Ref(), ten.ID); !slices.Equal(got, []string{auth.OwnerRoleID}) {
		t.Fatalf("所有者的角色：%v", got)
	}
	if addr.URL(ten) != "https://desk.example.com/t/acme/" {
		t.Fatalf("地址：%s", addr.URL(ten))
	}
	if n := count(t, env, "SELECT count(*) FROM activity_log WHERE event = 'tenant.created' AND target_id = ?", ten.ID); n != 1 {
		t.Fatalf("操作日志有 %d 条", n)
	}
}

func TestCreateReusesExistingMember(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	create := tenant.NewCreate(env.DB, env.MemberLogins, addr)
	first, err := create.Handle(ctx, apptest.Admin, acme)
	if err != nil {
		t.Fatal(err)
	}

	// 同一个邮箱成为第二个租户的所有者：使用已有的账号，姓名和密码不需要
	second, err := create.Handle(ctx, apptest.Admin, tenant.CreateParams{Name: "Globex", Slug: "globex", OwnerEmail: "Alice@acme.com"})
	if err != nil {
		t.Fatal(err)
	}
	if second.OwnerID != first.OwnerID {
		t.Fatalf("新建了另一个账号：%s 和 %s", first.OwnerID, second.OwnerID)
	}
	if n := count(t, env, "SELECT count(*) FROM members"); n != 1 {
		t.Fatalf("members 有 %d 行", n)
	}
}

func TestCreateRejectsAndRollsBack(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	create := tenant.NewCreate(env.DB, env.MemberLogins, addr)
	if _, err := create.Handle(ctx, apptest.Admin, acme); err != nil {
		t.Fatal(err)
	}
	disabled := tenant.CreateParams{Name: "Bob", Slug: "bob-team", OwnerEmail: "bob@example.com", OwnerName: "Bob", OwnerPassword: "bob-pass-1234"}
	if _, err := create.Handle(ctx, apptest.Admin, disabled); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE members SET disabled_at = ? WHERE id = (SELECT owner_id FROM tenants WHERE slug = 'bob-team')", time.Now().UTC()).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		p    tenant.CreateParams
		want error
	}{
		{"slug 已被使用", tenant.CreateParams{Name: "x", Slug: "acme", OwnerEmail: "carol@example.com", OwnerName: "Carol", OwnerPassword: "carol-pass-123"}, tenant.ErrSlugTaken},
		{"slug 是保留词", tenant.CreateParams{Name: "x", Slug: "admin", OwnerEmail: "alice@acme.com"}, tenant.ErrSlugReserved},
		{"新邮箱缺少密码", tenant.CreateParams{Name: "x", Slug: "dave", OwnerEmail: "dave@example.com", OwnerName: "Dave"}, member.ErrDetailsMissing},
		{"初始密码太短", tenant.CreateParams{Name: "x", Slug: "erin", OwnerEmail: "erin@example.com", OwnerName: "Erin", OwnerPassword: "short"}, login.ErrWeakPassword},
		{"所有者的账号已停用", tenant.CreateParams{Name: "x", Slug: "frank", OwnerEmail: "bob@example.com"}, member.ErrDisabled},
	}
	for _, tc := range tests {
		if _, err := create.Handle(ctx, apptest.Admin, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%s：得到 %v，期望 %v", tc.name, err, tc.want)
		}
	}
	// 失败的请求整体回滚：没有多出租户，也没有多出成员账号（slug 重复时 carol 的账号同样回滚）
	if n := count(t, env, "SELECT count(*) FROM tenants"); n != 2 {
		t.Fatalf("tenants 有 %d 行", n)
	}
	if n := count(t, env, "SELECT count(*) FROM members"); n != 2 {
		t.Fatalf("members 有 %d 行", n)
	}
}
