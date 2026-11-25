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
	"agents-desk/internal/push"
	"agents-desk/internal/role"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// TestDefaultAssignmentSettings 验证工作区级默认指派设置的校验、渠道优先级、默认团队/默认成员路由、
// 离职成员清理以及跨工作区隔离。
func TestDefaultAssignmentSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "assign-a", Slug: "assign-a", OwnerEmail: "owner@a.com", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherTn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "assign-b", Slug: "assign-b", OwnerEmail: "owner@b.com", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	ownerSub := identity.Subject{
		Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	otherOwnerSub := identity.Subject{
		Type: member.Type, ID: otherTn.OwnerID, Tenant: otherTn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	agentRole, err := role.NewCreate(env.DB, env.Roles).Handle(ctx, ownerSub, role.Params{Name: "客服", ConversationScope: auth.ScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, ownerSub, member.AddParams{
		Email: "agent@a.com", Name: "坐席甲", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	tmDefault, err := team.NewCreate(env.DB).Handle(ctx, ownerSub, team.Changes{Name: "默认团队", Assignment: team.RoundRobin})
	if err != nil {
		t.Fatal(err)
	}
	tmChannel, err := team.NewCreate(env.DB).Handle(ctx, ownerSub, team.Changes{Name: "渠道团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	tmOther, err := team.NewCreate(env.DB).Handle(ctx, otherOwnerSub, team.Changes{Name: "别家团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}

	actions := conversation.NewAssignmentSettingsActions(env.DB)

	// 1. 初始默认值为 none
	got, err := actions.Get(ctx, ownerSub)
	if err != nil || got.DefaultTarget != conversation.DefaultNone {
		t.Fatalf("初始设置应为 none：%+v %v", got, err)
	}

	// 2. 参数与跨工作区隔离校验
	if _, err := actions.Save(ctx, ownerSub, conversation.AssignmentSettings{DefaultTarget: conversation.DefaultTeam}); !errors.Is(err, conversation.ErrDefaultTeamRequired) {
		t.Fatalf("未传团队应报 ErrDefaultTeamRequired，实际：%v", err)
	}
	if _, err := actions.Save(ctx, ownerSub, conversation.AssignmentSettings{DefaultTarget: conversation.DefaultMember}); !errors.Is(err, conversation.ErrDefaultMemberRequired) {
		t.Fatalf("未传成员应报 ErrDefaultMemberRequired，实际：%v", err)
	}
	if _, err := actions.Save(ctx, ownerSub, conversation.AssignmentSettings{DefaultTarget: conversation.DefaultTeam, DefaultTeamID: tmOther.ID}); !errors.Is(err, team.ErrNotFound) {
		t.Fatalf("别的工作区的团队应报 team.ErrNotFound，实际：%v", err)
	}
	if _, err := actions.Save(ctx, ownerSub, conversation.AssignmentSettings{DefaultTarget: conversation.DefaultMember, DefaultMemberID: otherTn.OwnerID}); !errors.Is(err, member.ErrNotFound) {
		t.Fatalf("别的工作区的成员应报 member.ErrNotFound，实际：%v", err)
	}

	// 3. 配置工作区默认指派为 tmDefault；渠道未设默认团队时走工作区默认团队，渠道设了默认团队时优先渠道
	if _, err := actions.Save(ctx, ownerSub, conversation.AssignmentSettings{
		DefaultTarget: conversation.DefaultTeam, DefaultTeamID: tmDefault.ID,
	}); err != nil {
		t.Fatal(err)
	}
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	chPlain, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "无默认团队渠道"})
	if err != nil {
		t.Fatal(err)
	}
	chWithTeam, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "有默认团队渠道", TeamID: &tmChannel.ID})
	if err != nil {
		t.Fatal(err)
	}

	down := conversation.Downstream{Hub: hub, Roles: env.Roles}
	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	r1, err := recv.Handle(ctx, system, conversation.ReceiveParams{ChannelID: chPlain.ID, From: "u1", FromName: "客户1", Text: "hi", ExternalID: "e1"})
	if err != nil {
		t.Fatal(err)
	}
	c1, err := conversation.NewGet(env.DB).Handle(ctx, ownerSub, r1.ConversationID)
	if err != nil || c1.TeamID == nil || *c1.TeamID != tmDefault.ID {
		t.Fatalf("无默认团队渠道应回退到工作区默认团队 %s，实际：%+v %v", tmDefault.ID, c1, err)
	}
	r2, err := recv.Handle(ctx, system, conversation.ReceiveParams{ChannelID: chWithTeam.ID, From: "u2", FromName: "客户2", Text: "hi", ExternalID: "e2"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := conversation.NewGet(env.DB).Handle(ctx, ownerSub, r2.ConversationID)
	if err != nil || c2.TeamID == nil || *c2.TeamID != tmChannel.ID {
		t.Fatalf("渠道默认团队应优先于工作区默认设置：%+v %v", c2, err)
	}

	// 4. 配置工作区默认指派为成员 agent：新会话直接指派给 agent，记录 default 事件并触发 assigned 提醒
	if _, err := actions.Save(ctx, ownerSub, conversation.AssignmentSettings{
		DefaultTarget: conversation.DefaultMember, DefaultMemberID: agent.ID,
	}); err != nil {
		t.Fatal(err)
	}
	agentSubChan := hub.Subscribe(push.Channel(tn.ID, agent.ID))
	defer agentSubChan.Close()

	r3, err := recv.Handle(ctx, system, conversation.ReceiveParams{ChannelID: chPlain.ID, From: "u3", FromName: "客户3", Text: "need help", ExternalID: "e3"})
	if err != nil {
		t.Fatal(err)
	}
	c3, err := conversation.NewGet(env.DB).Handle(ctx, ownerSub, r3.ConversationID)
	if err != nil || c3.Assignee == nil || c3.Assignee.ID != agent.ID || c3.TeamID != nil {
		t.Fatalf("默认指派给成员失败：%+v %v", c3, err)
	}
	if ev := readAlert(t, agentSubChan); ev.Alert != push.AlertAssigned {
		t.Fatalf("默认指派给成员应触发 assigned 实时提醒，实际：%+v", ev)
	}
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, ownerSub, r3.ConversationID, 0, 20)
	if err != nil || len(items) != 2 || items[1].Kind != conversation.Event || items[1].Content.Data["reason"] != string(conversation.ReasonDefault) {
		t.Fatalf("时间线应包含 reason=default 的系统指派事件：%+v %v", items, err)
	}

	// 5. 移出默认成员后，工作区默认指派自动重置为 none
	hooks := member.Hooks{Removed: []member.RemovedHook{conversation.OnMemberLeft(down)}}
	if err := member.NewRemove(env.DB, env.Roles, hooks).Handle(ctx, ownerSub, agent.ID); err != nil {
		t.Fatal(err)
	}
	gotAfterRemove, err := actions.Get(ctx, ownerSub)
	if err != nil || gotAfterRemove.DefaultTarget != conversation.DefaultNone || gotAfterRemove.DefaultMemberID != "" {
		t.Fatalf("移出默认负责人后应重置为 none：%+v %v", gotAfterRemove, err)
	}
}
