package contact_test

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/sheet"
	"github.com/go-tenon/tenon/sheet/exports"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
	"agents-desk/internal/member"
	"agents-desk/internal/tag"
	"agents-desk/internal/tenant"
)

// TestContactExportCursorPagination 验证超过单批上限（>500 条）时游标分页导出完整且不漏行、标签匹配正确。
func TestContactExportCursorPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "exp-cursor", Slug: "exp-cursor", OwnerEmail: "owner@exp-cursor.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{
		Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	tg, err := tag.NewCreate(env.DB).Handle(ctx, sub, tag.Changes{Name: "大批量", Color: tag.Yellow})
	if err != nil {
		t.Fatal(err)
	}

	const totalContacts = 505
	baseAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	rows := make([]contact.Contact, totalContacts)
	for i := range totalContacts {
		at := baseAt.Add(time.Duration(i) * time.Second)
		rows[i] = contact.Contact{
			ID:           uuid.Must(uuid.NewV7()).String(),
			TenantID:     tn.ID,
			Number:       int64(i + 1),
			Kind:         contact.Lead,
			Name:         fmt.Sprintf("客户-%04d", i+1),
			Attributes:   map[string]any{},
			NotesDerived: map[string]any{},
			LastSeenAt:   &at,
		}
		rows[i].CreatedAt = at
		rows[i].UpdatedAt = at
	}
	if _, err := env.DB.NewInsert().Model(&rows).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 为最早入库的（第二批最后一行）和最新入库的（第一批第一行）联系人各打上标签，验证每批均正确查标签。
	for _, cid := range []string{rows[0].ID, rows[totalContacts-1].ID} {
		if _, err := env.DB.NewRaw(`INSERT INTO contact_tags (tenant_id, contact_id, tag_id, created_at) VALUES (?, ?, ?, ?)`,
			tn.ID, cid, tg.ID, baseAt).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	ops := operation.New(env.DB)
	exporter := exports.New(env.DB, ops, env.Files)
	action := contact.NewQueueExport(env.DB, nil, exporter)
	exp, err := action.Handle(ctx, sub, sheet.CSV, contact.Filter{}, contact.ByRecent, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := action.Job(ctx, contact.ExportRequest{ID: exp.ID, Subject: sub, Sort: contact.ByRecent}); err != nil {
		t.Fatal(err)
	}

	done, err := exporter.Get(ctx, sub, exp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != operation.StatusCompleted || done.Processed != totalContacts {
		t.Fatalf("跨批导出条数异常：%+v", done)
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
	if len(records) != totalContacts+1 {
		t.Fatalf("CSV 行数应为表头 + %d，实际 %d", totalContacts, len(records))
	}
	if !strings.Contains(strings.Join(records[1], ","), "大批量") || !strings.Contains(strings.Join(records[totalContacts], ","), "大批量") {
		t.Fatalf("首尾跨批联系人应均带标签：first=%v last=%v", records[1], records[totalContacts])
	}
}
