package channel_test

import (
	"context"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/tenant"
)

// TestListChannels 检查渠道只读列表按工作区隔离并保留已停用渠道（供筛选与报表使用）。
func TestListChannels(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	createTenant := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "alpha", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "beta", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	alphaSub := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	betaSub := identity.Subject{Type: identity.System, ID: "test", Tenant: beta.ID}
	createCh := channel.NewCreate(env.DB)

	ch1, err := createCh.Handle(ctx, alphaSub, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := createCh.Handle(ctx, alphaSub, channel.CreateParams{Kind: channel.Widget, Name: "官网插件"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewUpdate().Model((*channel.Channel)(nil)).Set("enabled = false").Where("id = ?", ch2.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := createCh.Handle(ctx, betaSub, channel.CreateParams{Kind: channel.Test, Name: "其他工作区渠道"}); err != nil {
		t.Fatal(err)
	}

	items, err := channel.NewList(env.DB).Handle(ctx, alphaSub)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != ch1.ID || !items[0].Enabled || items[1].ID != ch2.ID || items[1].Enabled {
		t.Fatalf("alpha channels=%+v", items)
	}
}
