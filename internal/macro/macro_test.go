package macro_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/macro"
	"agents-desk/internal/member"
	"agents-desk/internal/tag"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

func TestMacroCRUDPermissionsAndIsolation(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "B主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	inAlpha := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	inBeta := identity.Subject{Type: identity.System, ID: "test", Tenant: beta.ID}

	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminRole, err := env.Roles.RoleByName(ctx, "管理员", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	adminMember, err := addMember.Handle(ctx, inAlpha, member.AddParams{
		Email: "admin@alpha.com", Name: "管理员", Password: "password", RoleID: adminRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentA, err := addMember.Handle(ctx, inAlpha, member.AddParams{
		Email: "a@alpha.com", Name: "坐席A", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentB, err := addMember.Handle(ctx, inAlpha, member.AddParams{
		Email: "b@alpha.com", Name: "坐席B", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	subAdmin := identity.Subject{
		Type: member.Type, ID: adminMember.ID, Tenant: alpha.ID,
		Permissions: auth.WithBase([]string{string(auth.MacrosManage)}),
	}
	subA := identity.Subject{Type: member.Type, ID: agentA.ID, Tenant: alpha.ID, Permissions: auth.WithBase(nil)}
	subB := identity.Subject{Type: member.Type, ID: agentB.ID, Tenant: alpha.ID, Permissions: auth.WithBase(nil)}

	// 准备附带操作引用的团队、标签与会话属性
	activeTeam, err := team.NewCreate(env.DB).Handle(ctx, inAlpha, team.Changes{Name: "一线客服", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	disabledTeam, err := team.NewCreate(env.DB).Handle(ctx, inAlpha, team.Changes{Name: "旧团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := team.NewDisable(env.DB).Handle(ctx, inAlpha, disabledTeam.ID); err != nil {
		t.Fatal(err)
	}
	betaTeam, err := team.NewCreate(env.DB).Handle(ctx, inBeta, team.Changes{Name: "B团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	vipTag, err := tag.NewCreate(env.DB).Handle(ctx, inAlpha, tag.Changes{Name: "VIP", Color: tag.Yellow})
	if err != nil {
		t.Fatal(err)
	}
	betaTag, err := tag.NewCreate(env.DB).Handle(ctx, inBeta, tag.Changes{Name: "B标签", Color: tag.Red})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attribute.NewCreate(env.DB).Handle(ctx, inAlpha, attribute.CreateParams{
		Target: attribute.TargetConversation, Key: "issue_type", Name: "问题类型", Type: attribute.TypeSelect, Options: []string{"物流", "退款"},
	}); err != nil {
		t.Fatal(err)
	}

	create, get, list, update, del := macro.NewCreate(env.DB, env.Roles), macro.NewGet(env.DB), macro.NewList(env.DB), macro.NewUpdate(env.DB, env.Roles), macro.NewDelete(env.DB)

	// 1. 权限校验：无 macros:manage 不能创建共享快捷回复
	if _, err := create.Handle(ctx, subA, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "共享", Body: "hi",
	}); !errors.Is(err, macro.ErrForbidden) {
		t.Fatalf("普通坐席建共享快捷回复应返回 ErrForbidden：%v", err)
	}

	// 2. 管理员创建带附带操作的共享快捷回复（自动转小写 shortcut）
	high := conversation.High
	tomorrow := macro.SnoozeTomorrow
	shared, err := create.Handle(ctx, subAdmin, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "核对订单", Shortcut: "Order_Check",
		Body: "您好 {{contact.name | 朋友}}",
		Actions: macro.Actions{
			Team:          &conversation.TeamChange{Mode: conversation.TeamSet, ID: activeTeam.ID},
			Assignee:      &conversation.AssigneeChange{Mode: conversation.AssigneeMe},
			Priority:      &high,
			AddTagIDs:     []string{vipTag.ID, vipTag.ID}, // 重复标签自动去重
			SetAttributes: map[string]any{"issue_type": "物流"},
			Status:        &macro.StatusAction{Kind: conversation.StatusActionSnooze, Snooze: &tomorrow},
		},
	})
	if err != nil {
		t.Fatalf("创建共享快捷回复：%v", err)
	}
	if shared.Scope != macro.ScopeShared || shared.Shortcut != "order_check" || !shared.Editable || len(shared.Actions.AddTagIDs) != 1 {
		t.Fatalf("共享快捷回复结果异常：%+v", shared)
	}

	// 共享同快捷码冲突
	if _, err := create.Handle(ctx, subAdmin, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "重复", Shortcut: "order_check", Body: "dup",
	}); !errors.Is(err, macro.ErrShortcutTaken) {
		t.Fatalf("共享重复快捷码应返回 ErrShortcutTaken：%v", err)
	}

	// 3. 个人快捷回复与共享允许同快捷码，且不同成员间完全隔离
	personalA, err := create.Handle(ctx, subA, macro.CreateParams{
		Scope: macro.ScopePersonal, Title: "我的快捷", Shortcut: "order_check",
		Actions: macro.Actions{Status: &macro.StatusAction{Kind: conversation.StatusActionClose}}, // 纯附带操作允许空正文
	})
	if err != nil {
		t.Fatalf("坐席 A 建个人快捷回复：%v", err)
	}
	if personalA.Scope != macro.ScopePersonal || !personalA.Editable {
		t.Fatalf("坐席 A 个人快捷回复异常：%+v", personalA)
	}
	personalB, err := create.Handle(ctx, subB, macro.CreateParams{
		Scope: macro.ScopePersonal, Title: "B的快捷", Shortcut: "order_check", Body: "hello",
	})
	if err != nil {
		t.Fatalf("坐席 B 使用相同快捷码建个人快捷回复应成功：%v", err)
	}

	// A 列表可见共享（editable=false）与自己的个人（editable=true），看不到 B 的个人
	resA, err := list.Handle(ctx, subA, macro.ListInput{})
	if err != nil || resA.Total != 2 {
		t.Fatalf("坐席 A 列表期望 2 条：total=%d err=%v", resA.Total, err)
	}
	for _, it := range resA.Items {
		if it.ID == personalB.ID {
			t.Fatal("坐席 A 不应看到坐席 B 的个人快捷回复")
		}
		if it.ID == shared.ID && it.Editable {
			t.Fatal("坐席 A 对共享快捷回复不应有可编辑标记")
		}
	}
	if _, err := get.Handle(ctx, subA, personalB.ID); !errors.Is(err, macro.ErrNotFound) {
		t.Fatalf("读取他人个人快捷回复应返回 ErrNotFound：%v", err)
	}
	if _, err := update.Handle(ctx, subA, shared.ID, macro.UpdateParams{Title: "偷改", Body: "x"}); !errors.Is(err, macro.ErrForbidden) {
		t.Fatalf("无权修改共享快捷回复应返回 ErrForbidden：%v", err)
	}
	if err := del.Handle(ctx, subA, shared.ID); !errors.Is(err, macro.ErrForbidden) {
		t.Fatalf("无权删除共享快捷回复应返回 ErrForbidden：%v", err)
	}

	// 4. 跨工作区隔离
	betaMacro, err := create.Handle(ctx, inBeta, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "B共享", Shortcut: "order_check", Body: "beta",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := get.Handle(ctx, subAdmin, betaMacro.ID); !errors.Is(err, macro.ErrNotFound) {
		t.Fatalf("跨工作区读取应返回 ErrNotFound：%v", err)
	}

	// 5. 参数和附带操作校验
	if _, err := create.Handle(ctx, subA, macro.CreateParams{Scope: macro.ScopePersonal, Title: "空内容"}); !errors.Is(err, macro.ErrEmpty) {
		t.Fatalf("正文和操作均为空应返回 ErrEmpty：%v", err)
	}
	if _, err := create.Handle(ctx, subA, macro.CreateParams{Scope: macro.ScopePersonal, Title: "非法码", Shortcut: "-bad", Body: "x"}); !errors.Is(err, macro.ErrInvalidShortcut) {
		t.Fatalf("非法快捷码应返回 ErrInvalidShortcut：%v", err)
	}
	if _, err := create.Handle(ctx, subAdmin, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "停用团队", Body: "x",
		Actions: macro.Actions{Team: &conversation.TeamChange{Mode: conversation.TeamSet, ID: disabledTeam.ID}},
	}); !errors.Is(err, team.ErrDisabled) {
		t.Fatalf("引用停用团队应返回 team.ErrDisabled：%v", err)
	}
	if _, err := create.Handle(ctx, subAdmin, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "跨工作区团队", Body: "x",
		Actions: macro.Actions{Team: &conversation.TeamChange{Mode: conversation.TeamSet, ID: betaTeam.ID}},
	}); !errors.Is(err, team.ErrNotFound) {
		t.Fatalf("引用别的工作区团队应返回 team.ErrNotFound：%v", err)
	}
	if _, err := create.Handle(ctx, subAdmin, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "跨工作区标签", Body: "x",
		Actions: macro.Actions{AddTagIDs: []string{betaTag.ID}},
	}); !errors.Is(err, tag.ErrNotFound) {
		t.Fatalf("引用别的工作区标签应返回 tag.ErrNotFound：%v", err)
	}
	if _, err := create.Handle(ctx, subAdmin, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "未知属性", Body: "x",
		Actions: macro.Actions{SetAttributes: map[string]any{"not_exist": "v"}},
	}); !errors.Is(err, attribute.ErrUnknownKey) {
		t.Fatalf("未知属性应返回 attribute.ErrUnknownKey：%v", err)
	}
	if _, err := create.Handle(ctx, subAdmin, macro.CreateParams{
		Scope: macro.ScopeShared, Title: "缺预设", Body: "x",
		Actions: macro.Actions{Status: &macro.StatusAction{Kind: conversation.StatusActionSnooze}},
	}); !errors.Is(err, macro.ErrInvalidActions) {
		t.Fatalf("snooze 缺预设应返回 ErrInvalidActions：%v", err)
	}

	// 6. 管理员更新并删除共享快捷回复，验证操作日志记录（created + updated + deleted = 3 条）
	updated, err := update.Handle(ctx, subAdmin, shared.ID, macro.UpdateParams{
		Title: "核对订单 v2", Shortcut: "order_v2", Body: "更新后的正文",
	})
	if err != nil || updated.Title != "核对订单 v2" || updated.Shortcut != "order_v2" {
		t.Fatalf("更新共享快捷回复：%v %+v", err, updated)
	}
	if err := del.Handle(ctx, subAdmin, shared.ID); err != nil {
		t.Fatalf("删除共享快捷回复：%v", err)
	}
	logs, err := activity.List(ctx, env.DB, activity.Query{Tenant: alpha.ID, TargetType: "macro"})
	if err != nil || len(logs.Items) != 3 {
		t.Fatalf("共享快捷回复操作日志期望 3 条：len=%d err=%v", len(logs.Items), err)
	}

	// 7. 成员移出工作区钩子：仅清理被移出成员的个人快捷回复，保留共享及其他成员的个人快捷回复
	stillShared, err := create.Handle(ctx, subAdmin, macro.CreateParams{Scope: macro.ScopeShared, Title: "保留共享", Body: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	err = database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		return macro.OnMemberRemoved(ctx, tx, alpha.ID, agentA.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := get.Handle(ctx, subA, personalA.ID); !errors.Is(err, macro.ErrNotFound) {
		t.Fatalf("移出成员后个人快捷回复应被清理：%v", err)
	}
	if _, err := get.Handle(ctx, subB, personalB.ID); err != nil {
		t.Fatalf("其他成员的个人快捷回复不应受影响：%v", err)
	}
	if _, err := get.Handle(ctx, subAdmin, stillShared.ID); err != nil {
		t.Fatalf("共享快捷回复不应受影响：%v", err)
	}
}
