package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/tenant"
	"agents-desk/internal/widget"
)

func TestSeed(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	addr := tenant.Addresses{PublicURL: "http://localhost:8081"}
	reg := channel.DefaultRegistry()
	reg[channel.Widget] = widget.NewConnector(nil)

	// 第一次全部写入，第二次全部跳过
	for i, skipped := range []bool{false, true} {
		var out bytes.Buffer
		if err := seed(ctx, &out, env.DB, env.Roles, env.AdminLogins, env.MemberLogins, env.Files, env.Crypt, addr, reg, conversation.Downstream{Topics: []queue.Topic[conversation.Appended]{conversation.DeliverTopic}}); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 15 || slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "跳过") != skipped }) {
			t.Fatalf("第 %d 次：\n%s", i+1, out.String())
		}
	}

	// 全部账号都能用 password 登录
	for _, c := range []struct {
		logins *login.Realm
		email  string
	}{
		{env.AdminLogins, "admin@example.com"}, {env.MemberLogins, "owner@example.com"},
		{env.MemberLogins, "manager@example.com"}, {env.MemberLogins, "agent@example.com"},
	} {
		if _, err := c.logins.Password(ctx, login.Email(c.email), seedPassword); err != nil {
			t.Errorf("%s：%v", c.email, err)
		}
	}
	var n int
	if err := env.DB.NewRaw("SELECT count(*) FROM tenants WHERE slug = 'demo'").Scan(ctx, &n); err != nil || n != 1 {
		t.Fatalf("示例工作区：%d %v", n, err)
	}
	// 示例工作区中有三个成员，各有自己的角色
	var demo string
	if err := env.DB.NewRaw("SELECT id FROM tenants WHERE slug = 'demo'").Scan(ctx, &demo); err != nil {
		t.Fatal(err)
	}
	members, err := env.Roles.Members(ctx, demo)
	if err != nil || len(members) != 3 {
		t.Fatalf("示例工作区的成员：%d %v", len(members), err)
	}
	// 示例会话：10 个基础会话 + 1 个跨渠道合并演示会话 + 2 个待合并重复联系人会话，共 13 个，其中 10 个等待回复
	var convs, waiting int
	if err := env.DB.NewRaw("SELECT count(*), count(waiting_since) FROM conversations WHERE tenant_id = ? AND kind = 'service'", demo).Scan(ctx, &convs, &waiting); err != nil || convs != 13 || waiting != 10 {
		t.Fatalf("示例会话：%d 个，%d 个等待回复，%v", convs, waiting, err)
	}
	// 示例会话的全文搜索索引已就绪：中英文均可直接搜到
	sysDemo := identity.Subject{Type: identity.System, ID: "test", Tenant: demo}
	if res, err := conversation.NewSearch(env.DB).Handle(ctx, sysDemo, &conversation.SearchInput{Q: "银行卡"}); err != nil || res.Total != 1 {
		t.Fatalf("示例会话搜索“银行卡”：total=%d err=%v", res.Total, err)
	}
	if res, err := conversation.NewSearch(env.DB).Handle(ctx, sysDemo, &conversation.SearchInput{Q: "withdrawal"}); err != nil || res.Total != 1 {
		t.Fatalf("示例会话搜索 withdrawal：total=%d err=%v", res.Total, err)
	}
}
