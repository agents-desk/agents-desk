package flags_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/flags"
	"agents-desk/internal/tenant"
)

func TestFeatureFlagsManagement(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	store := flags.NewStore(env.DB)

	created, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{PublicURL: "http://localhost:8081"}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "测试工作区", Slug: "beta", OwnerEmail: "owner@example.com", OwnerName: "主管理", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}

	list := flags.NewList(env.DB, store)
	set := flags.NewSet(env.DB, store)
	clear := flags.NewClear(env.DB, store)

	// 初始列表返回目录中的全部开关
	items, err := list.Handle(ctx)
	if err != nil || len(items) != len(flags.Catalog) {
		t.Fatalf("开关列表：len=%d err=%v", len(items), err)
	}

	// 未知开关返回 ErrNotFound
	if _, err := set.Handle(ctx, apptest.Admin, flags.SetInput{Name: "unknown.flag"}); !errors.Is(err, flags.ErrNotFound) {
		t.Fatalf("未知开关应当返回 ErrNotFound，实际 %v", err)
	}

	// 为指定工作区开启默认关闭的实验开关
	in := flags.SetInput{Name: flags.WidgetVisitorUploads.Name}
	in.Body.Scope = flags.ScopeTenant
	in.Body.TenantID = created.ID
	in.Body.Mode = flags.ModeOn
	updated, err := set.Handle(ctx, apptest.Admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Overrides) != 1 || updated.Overrides[0].TenantSlug != "beta" || updated.Overrides[0].Mode != flags.ModeOn {
		t.Fatalf("工作区覆盖不符合预期：%+v", updated.Overrides)
	}
	if !store.Active(ctx, identity.Subject{Tenant: created.ID}, flags.WidgetVisitorUploads) {
		t.Fatal("工作区开启覆盖后应当返回 true")
	}

	// 全员按比例放量 25%
	in = flags.SetInput{Name: flags.WidgetVisitorUploads.Name}
	in.Body.Scope = flags.ScopeEveryone
	in.Body.Mode = flags.ModePercent
	in.Body.Percent = 25
	updated, err = set.Handle(ctx, apptest.Admin, in)
	if err != nil || updated.Everyone == nil || updated.Everyone.Mode != flags.ModePercent || updated.Everyone.Percent != 25 {
		t.Fatalf("全员比例设置：%+v err=%v", updated.Everyone, err)
	}

	// 清除工作区覆盖
	cleared, err := clear.Handle(ctx, apptest.Admin, flags.ClearInput{
		Name: flags.WidgetVisitorUploads.Name, Scope: flags.ScopeTenant, TenantID: created.ID,
	})
	if err != nil || len(cleared.Overrides) != 0 {
		t.Fatalf("清除覆盖后应当为空：%+v err=%v", cleared.Overrides, err)
	}

	// 检查操作日志已记录
	logs, err := activity.List(ctx, env.DB, activity.Query{Tenant: "", TargetType: "feature"})
	if err != nil || len(logs.Items) != 3 {
		t.Fatalf("期望 3 条功能开关操作日志，实际 %d err=%v", len(logs.Items), err)
	}
}
