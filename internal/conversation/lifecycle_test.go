package conversation_test

import (
	"context"
	"testing"
	"time"

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

// TestAutoCloseAndSLAReassignLifecycle 验证闲置会话自动关闭、SLA 超时自动改派（含冷却防抖与无其他在线成员跳过）
// 以及离职成员自动退出群聊并保留生命周期设置。
func TestAutoCloseAndSLAReassignLifecycle(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "life-a", Slug: "life-a", OwnerEmail: "owner@lifea.com", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	ownerSub := identity.Subject{
		Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}

	agentRole, err := role.NewCreate(env.DB, env.Roles).Handle(ctx, ownerSub, role.Params{Name: "全权限坐席", ConversationScope: auth.ScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	agentA, err := addMember.Handle(ctx, ownerSub, member.AddParams{Email: "a@lifea.com", Name: "坐席A", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	agentB, err := addMember.Handle(ctx, ownerSub, member.AddParams{Email: "b@lifea.com", Name: "坐席B", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	subA := identity.Subject{Type: member.Type, ID: agentA.ID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(agentRole.ConversationScope, agentRole.Permissions))}
	subB := identity.Subject{Type: member.Type, ID: agentB.ID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(agentRole.ConversationScope, agentRole.Permissions))}

	// 创建轮流分配团队并加入 A 与 B
	tm, err := team.NewCreate(env.DB).Handle(ctx, ownerSub, team.Changes{Name: "SLA 团队", Assignment: team.RoundRobin})
	if err != nil {
		t.Fatal(err)
	}
	putMember := team.NewPutMember(env.DB, env.Roles, env.MemberLogins, env.Files)
	if _, err := putMember.Handle(ctx, ownerSub, tm.ID, agentA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := putMember.Handle(ctx, ownerSub, tm.ID, agentB.ID); err != nil {
		t.Fatal(err)
	}

	setStatus := member.NewSetMyStatus(env.DB)
	if _, err := setStatus.Handle(ctx, subA, member.StatusSetting{Status: member.Online}); err != nil {
		t.Fatal(err)
	}
	if _, err := setStatus.Handle(ctx, subB, member.StatusSetting{Status: member.Online}); err != nil {
		t.Fatal(err)
	}

	// 开启闲置 12 小时自动关闭与 10 分钟 SLA 自动改派
	cfgActions := conversation.NewAssignmentSettingsActions(env.DB)
	if _, err := cfgActions.Save(ctx, ownerSub, conversation.AssignmentSettings{
		DefaultTarget:       conversation.DefaultTeam,
		DefaultTeamID:       tm.ID,
		AutoCloseAfterHours: 12,
		SLAReassignMinutes:  10,
	}); err != nil {
		t.Fatal(err)
	}

	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道", TeamID: &tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	reply := conversation.NewReply(env.DB, down)
	assign := conversation.NewAssign(env.DB, down)

	// 会话 1：坐席已回复（waiting_since 为空）且已过去 15 小时 -> 应自动关闭
	cIdle, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "c-idle", Text: "问题好了"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reply.Handle(ctx, subA, cIdle.ConversationID, conversation.ReplyBody{Text: "好的随时联系"}); err != nil {
		t.Fatal(err)
	}
	// 会话 2：客户最新消息等待 15 小时 -> waiting_since 非空，不应自动关闭；且满足 SLA 超时改派
	cSLA, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "c-sla", Text: "急需处理"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, ownerSub, cSLA.ConversationID, conversation.Assignment{AssigneeID: &agentA.ID}); err != nil {
		t.Fatal(err)
	}

	baseNow := time.Now().UTC().Truncate(time.Microsecond)
	oldTime := baseNow.Add(-15 * time.Hour)
	if _, err := env.DB.NewRaw("UPDATE conversations SET last_message_at = ? WHERE id = ?", oldTime, cIdle.ConversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE conversations SET waiting_since = ?, last_message_at = ? WHERE id = ?",
		baseNow.Add(-25*time.Minute), baseNow.Add(-25*time.Minute), cSLA.ConversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	// 1. 验证闲置自动关闭
	closedCount, err := conversation.AutoCloseIdle(ctx, env.DB, down, baseNow, 100)
	if err != nil {
		t.Fatal(err)
	}
	if closedCount != 1 {
		t.Fatalf("应关闭 1 条闲置会话，实际 %d", closedCount)
	}
	getConv := conversation.NewGet(env.DB)
	gotIdle, err := getConv.Handle(ctx, ownerSub, cIdle.ConversationID)
	if err != nil || gotIdle.Status != conversation.Closed {
		t.Fatalf("cIdle 状态应为 closed：%+v err=%v", gotIdle, err)
	}
	gotSLAStillOpen, err := getConv.Handle(ctx, ownerSub, cSLA.ConversationID)
	if err != nil || gotSLAStillOpen.Status != conversation.Open {
		t.Fatalf("waiting_since 非空的会话不应被自动关闭：%+v err=%v", gotSLAStillOpen, err)
	}
	// 确认事件 data.reason == "auto_close"
	var closeReason string
	if err := env.DB.NewRaw("SELECT content->'data'->>'reason' FROM timeline_items WHERE conversation_id = ? AND content->>'event' = 'conversation.closed' ORDER BY seq DESC LIMIT 1", cIdle.ConversationID).Scan(ctx, &closeReason); err != nil {
		t.Fatal(err)
	}
	if closeReason != "auto_close" {
		t.Fatalf("期望关闭原因为 auto_close，实际 %q", closeReason)
	}

	// 2. 验证 SLA 超时自动改派（从 A 改派给在线成员 B），同窗口再次执行应被冷却跳过
	reassigned, err := conversation.ReassignSLABreached(ctx, env.DB, down, baseNow, 100)
	if err != nil {
		t.Fatal(err)
	}
	if reassigned != 1 {
		t.Fatalf("应 SLA 改派 1 条会话，实际 %d", reassigned)
	}
	gotSLAAfter, err := getConv.Handle(ctx, ownerSub, cSLA.ConversationID)
	if err != nil || gotSLAAfter.Assignee == nil || gotSLAAfter.Assignee.ID != agentB.ID {
		t.Fatalf("期望负责人变为 agentB(%s)，实际 %+v", agentB.ID, gotSLAAfter.Assignee)
	}
	reassigned2, err := conversation.ReassignSLABreached(ctx, env.DB, down, baseNow.Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if reassigned2 != 0 {
		t.Fatalf("冷却窗口内再次执行不应重复乒乓改派，实际 %d", reassigned2)
	}

	// 3. 验证群聊退群清理 + OnMemberLeft 保留工作区生命周期阈值
	if _, err := cfgActions.Save(ctx, ownerSub, conversation.AssignmentSettings{
		DefaultTarget:       conversation.DefaultMember,
		DefaultMemberID:     agentB.ID,
		AutoCloseAfterHours: 12,
		SLAReassignMinutes:  10,
	}); err != nil {
		t.Fatal(err)
	}
	groupChat, err := conversation.NewCreateGroupChat(env.DB, down).Handle(ctx, ownerSub, conversation.CreateGroupChatParams{
		Name:      "值班群",
		MemberIDs: []string{agentB.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := member.NewRemove(env.DB, env.Roles, member.Hooks{Removed: []member.RemovedHook{conversation.OnMemberLeft(down)}}).Handle(ctx, ownerSub, agentB.ID); err != nil {
		t.Fatal(err)
	}
	// agentB 应已退出群聊
	var leftAt *time.Time
	if err := env.DB.NewRaw("SELECT left_at FROM conversation_participants WHERE conversation_id = ? AND participant_type = 'member' AND participant_id = ?", groupChat.ID, agentB.ID).Scan(ctx, &leftAt); err != nil {
		t.Fatal(err)
	}
	if leftAt == nil {
		t.Fatal("离开工作区后群聊参与者应记录 left_at")
	}
	// 生命周期阈值仍应保留
	gotCfg, err := cfgActions.Get(ctx, ownerSub)
	if err != nil {
		t.Fatal(err)
	}
	if gotCfg.DefaultTarget != conversation.DefaultNone || gotCfg.AutoCloseAfterHours != 12 || gotCfg.SLAReassignMinutes != 10 {
		t.Fatalf("默认指派重置时应保留生命周期设置：%+v", gotCfg)
	}
}
