package tenant_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/sheet"
	"github.com/go-tenon/tenon/sheet/exports"

	"agents-desk/internal/apptest"
	"agents-desk/internal/tenant"
)

func TestQueueExportTenants(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	addr := tenant.Addresses{PublicURL: "http://localhost:8081"}
	create := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, addr)
	for _, item := range []struct{ name, slug, email string }{
		{"示例工作区", "demo", "demo@example.com"},
		{"测试团队", "beta", "beta@example.com"},
	} {
		if _, err := create.Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: item.name, Slug: item.slug, OwnerEmail: item.email, OwnerName: item.slug, OwnerPassword: "password123",
		}); err != nil {
			t.Fatal(err)
		}
	}

	ops := operation.New(env.DB)
	exporter := exports.New(env.DB, ops, env.Files)
	action := tenant.NewQueueExport(env.DB, env.MemberLogins, addr, nil, exporter)

	// 发起后台导出
	exp, err := action.Handle(ctx, apptest.Admin, sheet.XLSX, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if exp.Status != operation.StatusPending {
		t.Fatalf("新发起的导出应当为 pending，实际 %s", exp.Status)
	}

	// 执行队列任务
	if err := action.Job(ctx, tenant.ExportRequest{ID: exp.ID, Subject: apptest.Admin}); err != nil {
		t.Fatal(err)
	}

	// 校验导出已完成且生成了下载地址
	done, err := exporter.Get(ctx, apptest.Admin, exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != operation.StatusCompleted || done.Total != 2 || done.Processed != 2 || !strings.HasSuffix(done.Filename, ".xlsx") || done.URL == "" {
		t.Fatalf("导出结果不符合预期：%+v", done)
	}

	// 校验完成后已给发起管理员发送站内通知
	notifs, err := notification.List(ctx, env.DB, notification.Query{Recipient: notification.RecipientOf(apptest.Admin)})
	if err != nil || len(notifs) != 1 || notifs[0].Event != "tenants.exported" {
		t.Fatalf("导出完成通知不符合预期：%+v err=%v", notifs, err)
	}
}
