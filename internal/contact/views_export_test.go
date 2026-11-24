package contact_test

import (
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/queue/queuetest"
	"github.com/go-tenon/tenon/savedview"
	"github.com/go-tenon/tenon/sheet"
	"github.com/go-tenon/tenon/sheet/exports"

	"agents-desk/internal/apptest"
	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/member"
	"agents-desk/internal/role"
	"agents-desk/internal/tag"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// TestContactViewsAndExport 验证联系人已保存筛选视图（权限、按可见范围计数、列表联动、跨工作区隔离）
// 以及联系人异步导出（按可见范围和筛选条件导出标签与自定义属性列、跨工作区隔离）。
func TestContactViewsAndExport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	hub := broadcast.New(broadcasttest.NewBackend(), nil)
	t.Cleanup(func() { _ = hub.Close() })

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "cv-a", Slug: "cv-a", OwnerEmail: "owner@cva.com", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherTn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "cv-b", Slug: "cv-b", OwnerEmail: "owner@cvb.com", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	ownerSub := identity.Subject{
		Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	otherSub := identity.Subject{
		Type: member.Type, ID: otherTn.OwnerID, Tenant: otherTn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}

	// 创建一个无 views:manage 权限且仅看所在团队的坐席
	agentRole, err := role.NewCreate(env.DB, env.Roles).Handle(ctx, ownerSub, role.Params{Name: "团队坐席", ConversationScope: auth.ScopeTeam})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, ownerSub, member.AddParams{
		Email: "agent@cva.com", Name: "坐席", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentSub := identity.Subject{
		Type: member.Type, ID: agent.ID, Tenant: tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeTeam, nil)),
	}
	tm, err := team.NewCreate(env.DB).Handle(ctx, ownerSub, team.Changes{Name: "一线"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := team.NewPutMember(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, ownerSub, tm.ID, agent.ID); err != nil {
		t.Fatal(err)
	}

	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	chTeam, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "团队渠道", TeamID: &tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	chHidden, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "隐藏渠道"})
	if err != nil {
		t.Fatal(err)
	}

	vipTag, err := tag.NewCreate(env.DB).Handle(ctx, ownerSub, tag.Changes{Name: "VIP", Color: tag.Yellow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attribute.NewCreate(env.DB).Handle(ctx, ownerSub, attribute.CreateParams{
		Target: attribute.TargetContact, Key: "company", Name: "公司", Type: attribute.TypeText,
	}); err != nil {
		t.Fatal(err)
	}

	var c1ID, c2ID string
	if err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		c1, err := contact.ResolveUser(ctx, tx, tn.ID, chTeam.ID, "ext1", "u-1", contact.Profile{Name: "张三", Email: "zhangsan@example.com"})
		if err != nil {
			return err
		}
		c1ID = c1.ID
		if _, _, err := contact.AddTag(ctx, tx, tn.ID, c1.ID, vipTag.ID); err != nil {
			return err
		}
		if _, err := contact.PatchAttributes(ctx, tx, tn.ID, c1.ID, map[string]any{"company": "星辰科技"}); err != nil {
			return err
		}
		c2, _, err := contact.ResolveIdentity(ctx, tx, tn.ID, chHidden.ID, "ext2", contact.Profile{Name: "李四"})
		if err != nil {
			return err
		}
		c2ID = c2.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 1. 联系人视图权限与 CRUD
	views := contact.NewViewActions(env.DB, hub, env.Roles)
	if _, err := views.Create(ctx, agentSub, contact.ViewCreateParams{
		Scope: savedview.ScopeShared, Name: "无权建共享", Config: contact.ViewConfig{},
	}); !errors.Is(err, savedview.ErrForbidden) {
		t.Fatalf("无 views:manage 权限不能建共享视图，实际：%v", err)
	}
	sharedView, err := views.Create(ctx, ownerSub, contact.ViewCreateParams{
		Scope:  savedview.ScopeShared,
		Name:   "VIP 用户",
		Config: contact.ViewConfig{Filter: contact.Filter{Kind: contact.User, TagID: vipTag.ID}, Sort: contact.ByRecent},
	})
	if err != nil {
		t.Fatal(err)
	}
	personalView, err := views.Create(ctx, agentSub, contact.ViewCreateParams{
		Scope:  savedview.ScopePersonal,
		Name:   "我的全部线索",
		Config: contact.ViewConfig{Filter: contact.Filter{Kind: contact.Lead}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 跨工作区无法修改或删除视图
	if _, err := views.Update(ctx, otherSub, sharedView.ID, contact.ViewUpdateParams{Name: "越权改名"}); !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("别的工作区不能修改视图，实际：%v", err)
	}

	// Summary 计数按调用者可见范围计算：owner 看得到 c1+c2，agent 只看得到团队渠道的 c1
	ownerSummary, err := contact.NewGetSummary(env.DB).Handle(ctx, ownerSub)
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerSummary.Views) != 1 || ownerSummary.Views[0].ID != sharedView.ID || ownerSummary.Views[0].Count != 1 {
		t.Fatalf("owner 视图汇总异常：%+v", ownerSummary.Views)
	}
	agentSummary, err := contact.NewGetSummary(env.DB).Handle(ctx, agentSub)
	if err != nil {
		t.Fatal(err)
	}
	if len(agentSummary.Views) != 2 || agentSummary.Views[0].Count != 1 || agentSummary.Views[1].ID != personalView.ID || agentSummary.Views[1].Count != 0 {
		t.Fatalf("agent 应看到共享视图(1)与个人线索视图(0，隐藏渠道不可见)：%+v", agentSummary.Views)
	}

	// 按视图查询列表
	listRes, err := contact.NewList(env.DB).Handle(ctx, ownerSub, &contact.ListInput{ViewID: sharedView.ID})
	if err != nil || len(listRes.Items) != 1 || listRes.Items[0].ID != c1ID {
		t.Fatalf("按视图查询列表结果异常：%+v %v", listRes, err)
	}

	// 2. 联系人后台导出：按可见范围与视图过滤，且输出自定义属性列与标签列
	ops := operation.New(env.DB)
	exporter := exports.New(env.DB, ops, env.Files)
	queueExp := contact.NewQueueExport(env.DB, hub, exporter)

	// 以 agentSub（仅有团队渠道可见范围，拥有导出权限）导出全部 CSV：只能导到 c1，不能导出 c2
	agentExportSub := identity.Subject{
		Type: member.Type, ID: agent.ID, Tenant: tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeTeam, []auth.TenantPermission{auth.TenantPermission(auth.ContactsExport)})),
	}
	exp, err := queueExp.Handle(ctx, agentExportSub, sheet.CSV, contact.Filter{}, contact.ByRecent, "")
	if err != nil {
		t.Fatal(err)
	}
	jobs := queuetest.Pending(t, env.DB, contact.ExportJob)
	if len(jobs) != 1 || jobs[0].ID != exp.ID {
		t.Fatalf("应发布一条联系人导出任务：%+v", jobs)
	}
	if err := queueExp.Job(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	done, err := exporter.Get(ctx, agentExportSub, exp.ID)
	if err != nil || done.Status != operation.StatusCompleted || done.Processed != 1 || done.FileID == uuid.Nil {
		t.Fatalf("导出任务应成功处理 1 条并生成文件：%+v %v", done, err)
	}
	// 别的工作区无法读取该导出记录
	if _, err := exporter.Get(ctx, otherSub, exp.ID); !errors.Is(err, operation.ErrNotFound) {
		t.Fatalf("别的工作区不应读取到导出任务，实际：%v", err)
	}

	// 校验生成的 CSV 内容：包含自定义属性“公司=星辰科技”、标签“VIP”，且不含隐藏渠道的“李四”
	rc, _, err := env.Files.Open(ctx, done.FileID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	records, err := csv.NewReader(rc).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("CSV 应包含表头和 1 行数据，实际 %d 行：%v", len(records), records)
	}
	header := strings.Join(records[0], ",")
	row := strings.Join(records[1], ",")
	if !strings.Contains(header, "公司") || !strings.Contains(row, "张三") || !strings.Contains(row, "VIP") || !strings.Contains(row, "星辰科技") || strings.Contains(row, "李四") {
		t.Fatalf("导出 CSV 内容不符：header=%q row=%q (c2=%s)", header, row, c2ID)
	}
}
