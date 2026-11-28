package conversation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/role"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// TestMemberHandoverAndIsolation 验证成员工作交接（未关闭会话 + 未解决工单 + 可选离职移出）及主管理员/自己/跨工作区保护。
func TestMemberHandoverAndIsolation(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "ho-a", Slug: "ho-a", OwnerEmail: "owner@hoa.com", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "ho-b", Slug: "ho-b", OwnerEmail: "owner@hob.com", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	sysA := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	ownerSub := identity.Subject{
		Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	betaSub := identity.Subject{
		Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}

	agentRole, err := role.NewCreate(env.DB, env.Roles).Handle(ctx, ownerSub, role.Params{Name: "全权限客服", ConversationScope: auth.ScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	alice, err := addMember.Handle(ctx, ownerSub, member.AddParams{Email: "alice@hoa.com", Name: "Alice", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := addMember.Handle(ctx, ownerSub, member.AddParams{Email: "bob@hoa.com", Name: "Bob", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}

	tmTarget, err := team.NewCreate(env.DB).Handle(ctx, ownerSub, team.Changes{Name: "接手团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	betaTeam, err := team.NewCreate(env.DB).Handle(ctx, betaSub, team.Changes{Name: "别家团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sysA, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}

	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	assign := conversation.NewAssign(env.DB, down)
	closeAct := conversation.NewClose(env.DB, down)

	// 创建 1 条打开会话 + 1 条已关闭会话 + 1 个未解决工单，全部指派给 Alice
	cOpen, err := receive.Handle(ctx, sysA, conversation.ReceiveParams{ChannelID: ch.ID, From: "u1", Text: "需要处理"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, ownerSub, cOpen.ConversationID, conversation.Assignment{AssigneeID: &alice.ID}); err != nil {
		t.Fatal(err)
	}
	cClosed, err := receive.Handle(ctx, sysA, conversation.ReceiveParams{ChannelID: ch.ID, From: "u2", Text: "已完结"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, ownerSub, cClosed.ConversationID, conversation.Assignment{AssigneeID: &alice.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := closeAct.Handle(ctx, ownerSub, cClosed.ConversationID); err != nil {
		t.Fatal(err)
	}
	tk, err := conversation.NewCreateTicket(env.DB, down).Handle(ctx, ownerSub, conversation.CreateTicketParams{
		Title:      "账号问题",
		AssigneeID: &alice.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	hooks := member.Hooks{Removed: []member.RemovedHook{conversation.OnMemberLeft(down)}}
	handover := conversation.NewHandover(env.DB, env.Roles, hooks, down)

	// 参数与权限保护边界
	if _, err := handover.Handle(ctx, ownerSub, alice.ID, conversation.HandoverParams{}); !errors.Is(err, conversation.ErrHandoverTargetRequired) {
		t.Fatalf("空目标应返回 ErrHandoverTargetRequired，实际 %v", err)
	}
	if _, err := handover.Handle(ctx, ownerSub, alice.ID, conversation.HandoverParams{TargetMemberID: alice.ID}); !errors.Is(err, conversation.ErrHandoverSameMember) {
		t.Fatalf("转给自己应返回 ErrHandoverSameMember，实际 %v", err)
	}
	if _, err := handover.Handle(ctx, ownerSub, alpha.OwnerID, conversation.HandoverParams{TargetMemberID: bob.ID, RemoveAfter: true}); !errors.Is(err, member.ErrSelf) {
		t.Fatalf("移出自己应返回 ErrSelf，实际 %v", err)
	}
	// 跨工作区成员与团队拦截
	if _, err := handover.Handle(ctx, betaSub, alice.ID, conversation.HandoverParams{TargetMemberID: beta.OwnerID}); !errors.Is(err, member.ErrNotFound) {
		t.Fatalf("跨工作区交接应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := handover.Handle(ctx, ownerSub, alice.ID, conversation.HandoverParams{TargetMemberID: bob.ID, TargetTeamID: betaTeam.ID}); !errors.Is(err, team.ErrNotFound) {
		t.Fatalf("跨工作区目标团队应返回 team.ErrNotFound，实际 %v", err)
	}

	// 执行交接并移出 Alice
	res, err := handover.Handle(ctx, ownerSub, alice.ID, conversation.HandoverParams{
		TargetMemberID: bob.ID,
		TargetTeamID:   tmTarget.ID,
		RemoveAfter:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TransferredConversations != 1 || res.TransferredTickets != 1 || !res.Removed {
		t.Fatalf("交接结果异常：%+v", res)
	}

	gotOpen, err := conversation.NewGet(env.DB).Handle(ctx, ownerSub, cOpen.ConversationID)
	if err != nil || gotOpen.Assignee == nil || gotOpen.Assignee.ID != bob.ID || gotOpen.TeamID == nil || *gotOpen.TeamID != tmTarget.ID {
		t.Fatalf("打开会话未正确转给 Bob 和目标团队：%+v err=%v", gotOpen, err)
	}
	// 已关闭会话必须保留 Alice 历史归因
	gotClosed, err := conversation.NewGet(env.DB).Handle(ctx, ownerSub, cClosed.ConversationID)
	if err != nil || gotClosed.Assignee == nil || gotClosed.Assignee.ID != alice.ID {
		t.Fatalf("已关闭会话应保留原负责人 Alice：%+v err=%v", gotClosed, err)
	}
	// 工单应转给 Bob
	var tkAssignee string
	if err := env.DB.NewRaw("SELECT assignee_id FROM conversations WHERE id = ?", tk.ID).Scan(ctx, &tkAssignee); err != nil {
		t.Fatal(err)
	}
	if tkAssignee != bob.ID {
		t.Fatalf("工单应转给 Bob(%s)，实际 %q", bob.ID, tkAssignee)
	}
	// Alice 已退出工作区
	if _, err := member.RoleOf(ctx, env.Roles, identity.Ref{Type: member.Type, ID: alice.ID}, alpha.ID); !errors.Is(err, member.ErrNotFound) {
		t.Fatalf("Alice 应已移出工作区，实际 err=%v", err)
	}
}
