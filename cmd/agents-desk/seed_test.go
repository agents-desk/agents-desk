package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/apptest"
	"agents-desk/internal/tenant"
)

func TestSeed(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	addr := tenant.Addresses{PublicURL: "http://localhost:8081"}

	// 第一次全部写入，第二次全部跳过
	for i, skipped := range []bool{false, true} {
		var out bytes.Buffer
		if err := seed(ctx, &out, env.DB, env.AdminLogins, env.MemberLogins, env.Files, addr); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 6 || slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "跳过") != skipped }) {
			t.Fatalf("第 %d 次：\n%s", i+1, out.String())
		}
	}

	// 全部账号都能用 password 登录
	for _, c := range []struct {
		logins *login.Realm
		email  string
	}{
		{env.AdminLogins, "admin@example.com"}, {env.MemberLogins, "owner@example.com"},
		{env.MemberLogins, "manager@example.com"}, {env.MemberLogins, "lead@example.com"}, {env.MemberLogins, "agent@example.com"},
	} {
		if _, err := c.logins.Password(ctx, login.Email(c.email), seedPassword); err != nil {
			t.Errorf("%s：%v", c.email, err)
		}
	}
	var n int
	if err := env.DB.NewRaw("SELECT count(*) FROM tenants WHERE slug = 'demo'").Scan(ctx, &n); err != nil || n != 1 {
		t.Fatalf("示例工作区：%d %v", n, err)
	}
	// 示例工作区中有四个成员，各有自己的角色
	var demo string
	if err := env.DB.NewRaw("SELECT id FROM tenants WHERE slug = 'demo'").Scan(ctx, &demo); err != nil {
		t.Fatal(err)
	}
	members, err := env.Roles.Members(ctx, demo)
	if err != nil || len(members) != 4 {
		t.Fatalf("示例工作区的成员：%d %v", len(members), err)
	}
}
