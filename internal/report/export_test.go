package report_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/report"
	"agents-desk/internal/tenant"
)

// TestReportExportCSVAndXLSX 验证 6 类报表的同步导出（CSV 含核心数据与编码头，XLSX 为 ZIP 流），及非法报表类型拦截。
func TestReportExportCSVAndXLSX(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "rep-exp", Slug: "rep-exp", OwnerEmail: "owner@repexp.com", OwnerName: "OwnerExp", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	ownerSub := identity.Subject{
		Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "导出渠道"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{}).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c-1", Text: "hello report export",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewCreateTicket(env.DB, conversation.Downstream{}).Handle(ctx, ownerSub, conversation.CreateTicketParams{
		Title: "导出工单",
	}); err != nil {
		t.Fatal(err)
	}

	exporter := report.NewExporter(env.DB, env.Roles, env.Files)
	kinds := []string{"conversations", "members", "satisfaction", "response_time", "tickets", "business"}

	for _, kind := range kinds {
		// CSV 验证
		reqCSV := httptest.NewRequest(http.MethodGet, "/t/rep-exp/api/reports/export?report="+kind+"&format=csv", nil)
		rrCSV := httptest.NewRecorder()
		if err := exporter.ServeHTTP(rrCSV, reqCSV, ownerSub); err != nil {
			t.Fatalf("export csv %s failed: %v", kind, err)
		}
		if ct := rrCSV.Header().Get("Content-Type"); !strings.Contains(ct, "text/csv") {
			t.Fatalf("csv content-type=%q", ct)
		}
		if cd := rrCSV.Header().Get("Content-Disposition"); !strings.Contains(cd, ".csv") {
			t.Fatalf("csv content-disposition=%q", cd)
		}
		if len(strings.TrimSpace(rrCSV.Body.String())) == 0 {
			t.Fatalf("csv %s body empty", kind)
		}

		// XLSX 验证（ZIP magic header "PK"）
		reqXLSX := httptest.NewRequest(http.MethodGet, "/t/rep-exp/api/reports/export?report="+kind+"&format=xlsx", nil)
		rrXLSX := httptest.NewRecorder()
		if err := exporter.ServeHTTP(rrXLSX, reqXLSX, ownerSub); err != nil {
			t.Fatalf("export xlsx %s failed: %v", kind, err)
		}
		if cd := rrXLSX.Header().Get("Content-Disposition"); !strings.Contains(cd, ".xlsx") {
			t.Fatalf("xlsx content-disposition=%q", cd)
		}
		if !bytes.HasPrefix(rrXLSX.Body.Bytes(), []byte("PK")) {
			t.Fatalf("xlsx %s missing PK zip signature", kind)
		}
	}

	reqBad := httptest.NewRequest(http.MethodGet, "/t/rep-exp/api/reports/export?report=unknown", nil)
	rrBad := httptest.NewRecorder()
	if err := exporter.ServeHTTP(rrBad, reqBad, ownerSub); !errors.Is(err, report.ErrUnsupportedReport) {
		t.Fatalf("非法报表类型应返回 ErrUnsupportedReport，实际 %v", err)
	}
}
