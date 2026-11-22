package audit_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/audit"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tenant"
	"agents-desk/locales"
)

func TestListPlatformAuditLog(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	bundle := i18n.MustNew("zh-CN", locales.FS)
	ctx := i18n.WithLocale(context.Background(), bundle, "zh-CN")

	// 创建平台管理员并执行若干平台动作，都会写入平台域（tenant = ""）的操作日志
	ops, err := platformadmin.NewCreate(env.DB, env.AdminLogins).Handle(ctx, identity.Subject{Type: identity.System, ID: "cli"}, "运维小明", "ops@example.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	actor := identity.Subject{Type: platformadmin.Type, ID: ops.ID}
	created, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{PublicURL: "http://localhost:8081"}).Handle(ctx, actor, tenant.CreateParams{
		Name: "测试工作区", Slug: "acme", OwnerEmail: "owner@example.com", OwnerName: "主管理", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.NewDisable(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, actor, created.ID); err != nil {
		t.Fatal(err)
	}

	list := audit.NewList(env.DB)

	// 全部日志按从新到旧排列，包含中文译文和操作人姓名
	items, err := list.Handle(ctx, audit.ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("期望 3 条日志，实际 %d：%+v", len(items), items)
	}
	if items[0].Event != "tenant.disabled" || items[0].Actor != "运维小明" || !strings.Contains(items[0].Text, "测试工作区") || items[0].Href != "/tenants" {
		t.Fatalf("最新一条日志不符合预期：%+v", items[0])
	}
	if items[1].Event != "tenant.created" || !strings.Contains(items[1].Text, "acme") {
		t.Fatalf("第二条日志不符合预期：%+v", items[1])
	}
	if items[2].Event != "platform_admin.created" || items[2].Actor != "" || !strings.Contains(items[2].Text, "运维小明") || items[2].Href != "/admins" {
		t.Fatalf("第三条日志不符合预期：%+v", items[2])
	}

	// 按对象类型筛选
	onlyTenants, err := list.Handle(ctx, audit.ListInput{TargetType: "tenant"})
	if err != nil || len(onlyTenants) != 2 {
		t.Fatalf("按 tenant 筛选：len=%d err=%v", len(onlyTenants), err)
	}

	// 游标翻页（before）
	page1, err := list.Handle(ctx, audit.ListInput{Limit: 2})
	if err != nil || len(page1) != 2 {
		t.Fatalf("第一页：len=%d err=%v", len(page1), err)
	}
	page2, err := list.Handle(ctx, audit.ListInput{Limit: 2, Before: page1[1].ID})
	if err != nil || len(page2) != 1 || page2[0].ID != items[2].ID {
		t.Fatalf("第二页：%+v err=%v", page2, err)
	}

	// 英文语境下返回英文译文
	enCtx := i18n.WithLocale(context.Background(), bundle, "en")
	enItems, err := list.Handle(enCtx, audit.ListInput{Limit: 1})
	if err != nil || len(enItems) != 1 || !strings.Contains(enItems[0].Text, "Disabled workspace") {
		t.Fatalf("英文日志译文：%+v err=%v", enItems, err)
	}
}
