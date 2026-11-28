package conversation_test

import (
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/queue/queuetest"
	"github.com/go-tenon/tenon/sheet"
	"github.com/go-tenon/tenon/sheet/exports"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/role"
	"agents-desk/internal/tenant"
)

// TestConversationAsyncExportAndVisibility 验证会话后台导出权限校验、按 visible() 可见范围导出及跨工作区隔离。
func TestConversationAsyncExportAndVisibility(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "ce-a", Slug: "ce-a", OwnerEmail: "owner@cea.com", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "ce-b", Slug: "ce-b", OwnerEmail: "owner@ceb.com", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	sysA := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	sysB := identity.Subject{Type: identity.System, ID: "test", Tenant: beta.ID}
	ownerSub := identity.Subject{
		Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}

	// 创建一个仅看自己会话但具备 reports:read 的角色，以及一个没有 reports:read 权限的角色
	scopedRole, err := role.NewCreate(env.DB, env.Roles).Handle(ctx, ownerSub, role.Params{
		Name: "受限报表员", ConversationScope: auth.ScopeAssigned, Permissions: []auth.TenantPermission{auth.TenantPermission(auth.ReportsRead)},
	})
	if err != nil {
		t.Fatal(err)
	}
	agentA, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, ownerSub, member.AddParams{
		Email: "a@cea.com", Name: "AgentA", Password: "password", RoleID: scopedRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	subScoped := identity.Subject{
		Type: member.Type, ID: agentA.ID, Tenant: alpha.ID,
		Permissions: auth.WithBase(auth.RolePermissions(scopedRole.ConversationScope, scopedRole.Permissions)),
	}
	subNoExport := identity.Subject{
		Type: member.Type, ID: agentA.ID, Tenant: alpha.ID,
		Permissions: auth.WithBase(nil),
	}

	chA, err := channel.NewCreate(env.DB).Handle(ctx, sysA, channel.CreateParams{Kind: channel.Test, Name: "渠道A"})
	if err != nil {
		t.Fatal(err)
	}
	chB, err := channel.NewCreate(env.DB).Handle(ctx, sysB, channel.CreateParams{Kind: channel.Test, Name: "渠道B"})
	if err != nil {
		t.Fatal(err)
	}

	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	assign := conversation.NewAssign(env.DB, down)
	cMine, err := receive.Handle(ctx, sysA, conversation.ReceiveParams{ChannelID: chA.ID, From: "c-mine", Text: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, ownerSub, cMine.ConversationID, conversation.Assignment{AssigneeID: &agentA.ID}); err != nil {
		t.Fatal(err)
	}
	cOther, err := receive.Handle(ctx, sysA, conversation.ReceiveParams{ChannelID: chA.ID, From: "c-other", Text: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, ownerSub, cOther.ConversationID, conversation.Assignment{AssigneeID: &alpha.OwnerID}); err != nil {
		t.Fatal(err)
	}
	// beta 工作区造一条会话用于隔离校验
	if _, err := receive.Handle(ctx, sysB, conversation.ReceiveParams{ChannelID: chB.ID, From: "c-beta", Text: "beta"}); err != nil {
		t.Fatal(err)
	}

	exporter := exports.New(env.DB, operation.New(env.DB), env.Files, exports.WithDisk(env.Files.PrivateDisk()))
	queueExp := conversation.NewQueueExport(env.DB, hub, exporter)

	// 无 reports:read 权限拒绝导出
	if _, err := queueExp.Handle(ctx, subNoExport, sheet.CSV, conversation.ExportQuery{Inbox: conversation.InboxAll}); !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("无 reports:read 应拒绝导出，实际 %v", err)
	}

	// ScopeAssigned 成员发起全工作区导出，执行 Job 后表格中只能出现 cMine，绝不出现 cOther 与 beta
	exp, err := queueExp.Handle(ctx, subScoped, sheet.CSV, conversation.ExportQuery{Inbox: conversation.InboxAll})
	if err != nil {
		t.Fatal(err)
	}
	pending := queuetest.Pending(t, env.DB, conversation.ExportJob)
	if len(pending) != 1 {
		t.Fatalf("期望排队 1 个导出任务，实际 %d", len(pending))
	}
	if err := queueExp.Job(ctx, pending[0]); err != nil {
		t.Fatal(err)
	}

	done, err := exporter.Get(ctx, subScoped, exp.ID)
	if err != nil {
		t.Fatal(err)
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
	if len(records) != 2 || records[1][0] != cMine.ConversationID {
		t.Fatalf("导出行数或可见范围异常（只应导出 1 条数据）：%v", records)
	}
	rawCSV := strings.Join(records[1], ",")
	if strings.Contains(rawCSV, cOther.ConversationID) {
		t.Fatalf("越权导出了同事会话 %s", cOther.ConversationID)
	}
}
