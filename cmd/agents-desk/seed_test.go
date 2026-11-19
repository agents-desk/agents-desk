package main

import (
	"bytes"
	"context"
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

	// 第一次写入，第二次全部跳过
	for i, want := range []string{"已创建", "已存在，跳过"} {
		var out bytes.Buffer
		if err := seed(ctx, &out, env.DB, env.AdminLogins, env.MemberLogins, addr); err != nil {
			t.Fatal(err)
		}
		if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 2 || !strings.Contains(lines[0], want) || !strings.Contains(lines[1], want) {
			t.Fatalf("第 %d 次：\n%s", i+1, out.String())
		}
	}

	// 两个账号都能用 password 登录
	for _, c := range []struct {
		logins *login.Realm
		email  string
	}{{env.AdminLogins, "admin@example.com"}, {env.MemberLogins, "owner@example.com"}} {
		if _, err := c.logins.Password(ctx, login.Email(c.email), seedPassword); err != nil {
			t.Errorf("%s：%v", c.email, err)
		}
	}
	var n int
	if err := env.DB.NewRaw("SELECT count(*) FROM tenants WHERE slug = 'demo'").Scan(ctx, &n); err != nil || n != 1 {
		t.Fatalf("示例工作区：%d %v", n, err)
	}
}
