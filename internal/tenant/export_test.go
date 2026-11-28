package tenant_test

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/sheet"
	"github.com/go-tenon/tenon/sheet/exports"

	"agents-desk/internal/apptest"
	"agents-desk/internal/member"
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
	action := tenant.NewQueueExport(env.DB, addr, nil, exporter)

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

// TestQueueExportTenantsCursorPagination 验证超过 500 条工作区时游标分页与批量邮箱查询完整无遗漏。
func TestQueueExportTenantsCursorPagination(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	const totalTenants = 505
	baseAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	ownerID := uuid.Must(uuid.NewV7()).String()
	m := &member.Member{
		ID: ownerID, Name: "BatchOwner", UILanguage: member.UILanguage("zh-CN"), Language: "zh-CN",
		SendShortcut: member.SendEnter, NotificationPreferences: member.DefaultNotificationPreferences(),
		CreatedAt: baseAt, UpdatedAt: baseAt,
	}
	if _, err := env.DB.NewInsert().Model(m).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO auth_identifiers (subject_type, tenant, kind, value, subject_id, verified_at, created_at)
		VALUES (?, '', ?, 'batch-owner@example.com', ?, ?, ?)`,
		string(member.Type), login.KindEmail, ownerID, baseAt, baseAt).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	tenants := make([]tenant.Tenant, totalTenants)
	for i := range totalTenants {
		at := baseAt.Add(time.Duration(i) * time.Second)
		tenants[i] = tenant.Tenant{
			ID:                uuid.Must(uuid.NewV7()).String(),
			Slug:              fmt.Sprintf("batch-%04d", i),
			Name:              fmt.Sprintf("工作区-%04d", i),
			OwnerID:           ownerID,
			ReopenWindowHours: tenant.DefaultReopenWindowHours,
			NextContactNumber: 1,
			NextTicketNumber:  1,
			CreatedAt:         at,
			UpdatedAt:         at,
		}
	}
	if _, err := env.DB.NewInsert().Model(&tenants).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	ops := operation.New(env.DB)
	exporter := exports.New(env.DB, ops, env.Files)
	action := tenant.NewQueueExport(env.DB, tenant.Addresses{PublicURL: "http://localhost:8081"}, nil, exporter)
	exp, err := action.Handle(ctx, apptest.Admin, sheet.CSV, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := action.Job(ctx, tenant.ExportRequest{ID: exp.ID, Subject: apptest.Admin}); err != nil {
		t.Fatal(err)
	}
	done, err := exporter.Get(ctx, apptest.Admin, exp.ID)
	if err != nil || done.Processed != totalTenants {
		t.Fatalf("跨批工作区导出失败：%+v err=%v", done, err)
	}
	rc, _, err := env.Files.Open(ctx, done.FileID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	records, err := csv.NewReader(rc).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != totalTenants+1 {
		t.Fatalf("CSV 行数应为表头 + %d，实际 %d", totalTenants, len(records))
	}
	if !strings.Contains(strings.Join(records[1], ","), "batch-owner@example.com") ||
		!strings.Contains(strings.Join(records[totalTenants], ","), "batch-owner@example.com") {
		t.Fatalf("首尾跨批工作区应均包含管理员邮箱：first=%v last=%v", records[1], records[totalTenants])
	}
}
