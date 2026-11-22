package conversation_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tag"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// TestReplyAndApplyActionsAtomicity 验证回复附带操作与纯批量操作在单事务中原子提交，以及权限不足时整笔回滚。
func TestReplyAndApplyActionsAtomicity(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	alpha, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	inAlpha := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}

	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminRole, err := env.Roles.RoleByName(ctx, "管理员", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.MemberLogins, env.Files)
	adminM, err := addMember.Handle(ctx, inAlpha, member.AddParams{Email: "admin@alpha.com", Name: "管理员", Password: "password", RoleID: adminRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	agentM, err := addMember.Handle(ctx, inAlpha, member.AddParams{Email: "agent@alpha.com", Name: "坐席", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}

	team1, err := team.NewCreate(env.DB).Handle(ctx, inAlpha, team.Changes{Name: "一线", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	team2, err := team.NewCreate(env.DB).Handle(ctx, inAlpha, team.Changes{Name: "二线", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := team.NewPutMember(env.DB, env.MemberLogins, env.Files).Handle(ctx, inAlpha, team1.ID, agentM.ID); err != nil {
		t.Fatal(err)
	}

	refundTag, err := tag.NewCreate(env.DB).Handle(ctx, inAlpha, tag.Changes{Name: "退款", Color: tag.DeepOrange})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attribute.NewCreate(env.DB).Handle(ctx, inAlpha, attribute.CreateParams{
		Target: attribute.TargetConversation, Key: "issue_type", Name: "问题类型", Type: attribute.TypeSelect, Options: []string{"退款", "物流"},
	}); err != nil {
		t.Fatal(err)
	}

	ch := &channel.Channel{ID: "019a0000-0000-7000-8000-000000000001", TenantID: alpha.ID, Kind: channel.Test, Name: "测试渠道", TeamID: &team1.ID, Settings: map[string]any{}, Enabled: true}
	if _, err := env.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	down := conversation.Downstream{}
	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	reply := conversation.NewReply(env.DB, down)
	applyAct := conversation.NewApplyActions(env.DB, down)
	getConv := conversation.NewGet(env.DB)
	timeline := conversation.NewTimeline(env.DB)

	subAdmin := identity.Subject{
		Type: member.Type, ID: adminM.ID, Tenant: alpha.ID,
		Permissions: auth.WithBase([]string{string(auth.ConversationsReadAll), string(auth.ConversationsAssign)}),
	}
	subAgent := identity.Subject{
		Type: member.Type, ID: agentM.ID, Tenant: alpha.ID,
		Permissions: auth.WithBase([]string{string(auth.ConversationsReadTeam)}),
	}

	// 会话 1：坐席无 conversations:assign 权限，尝试在回复时把未指派会话转派给他人 -> 整笔事务回滚（消息也不写入）
	r1, err := recv.Handle(ctx, inAlpha, conversation.ReceiveParams{ChannelID: ch.ID, From: "u1", ExternalID: "m1", Text: "需要帮助"})
	if err != nil {
		t.Fatal(err)
	}
	high := conversation.High
	_, err = reply.Handle(ctx, subAgent, r1.ConversationID, conversation.ReplyBody{
		Text: "转给管理员处理",
		Actions: &conversation.Actions{
			Priority:  &high,
			AddTagIDs: []string{refundTag.ID},
			Assignee:  &conversation.AssigneeChange{Mode: conversation.AssigneeMember, ID: adminM.ID},
		},
	})
	if !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("无权改派应返回 ErrForbidden：%v", err)
	}
	// 验证整笔回滚：时间线仍只有客户那 1 条消息，优先级和标签均未变
	items1, err := timeline.Handle(ctx, subAgent, r1.ConversationID, 0, 50)
	if err != nil || len(items1) != 1 {
		t.Fatalf("失败回滚后时间线应保持 1 条：len=%d err=%v", len(items1), err)
	}
	conv1, err := getConv.Handle(ctx, subAgent, r1.ConversationID)
	if err != nil || conv1.Priority != conversation.Normal || len(conv1.Tags) != 0 {
		t.Fatalf("失败回滚后会话属性不应被修改：%+v err=%v", conv1, err)
	}

	// 会话 1：管理员单事务回复 + 改优先级 + 加标签 + 改属性 + 显式转派 + 关闭
	_, err = reply.Handle(ctx, subAdmin, r1.ConversationID, conversation.ReplyBody{
		Text: "已为您加急处理并关闭",
		Actions: &conversation.Actions{
			Priority:      &high,
			AddTagIDs:     []string{refundTag.ID},
			SetAttributes: map[string]any{"issue_type": "退款"},
			Team:          &conversation.TeamChange{Mode: conversation.TeamSet, ID: team2.ID},
			Assignee:      &conversation.AssigneeChange{Mode: conversation.AssigneeMe},
			Status:        &conversation.StatusAction{Kind: conversation.StatusActionClose},
		},
	})
	if err != nil {
		t.Fatalf("原子回复与附带操作失败：%v", err)
	}
	conv1, err = getConv.Handle(ctx, subAdmin, r1.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if conv1.Status != conversation.Closed || conv1.Priority != conversation.High ||
		conv1.TeamID == nil || *conv1.TeamID != team2.ID ||
		conv1.Assignee == nil || conv1.Assignee.ID != adminM.ID ||
		len(conv1.Tags) != 1 || conv1.Attributes["issue_type"] != "退款" {
		t.Fatalf("原子回复后会话状态不符合预期：%+v", conv1)
	}
	// 检查时间线中不会产生重复的“回复即领取”事件（显式指派只记 1 条 assigned）
	items1, err = timeline.Handle(ctx, subAdmin, r1.ConversationID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	assignedCount := 0
	for _, it := range items1 {
		if it.Kind == conversation.Event && it.Content.Event == "conversation.assigned" {
			assignedCount++
		}
	}
	if assignedCount != 1 {
		t.Fatalf("显式附带指派时应只产生 1 条指派事件，实际 %d", assignedCount)
	}
	// 检查服务轮次记录中已记录本轮首次回复时间、首次回复人与关闭时间
	var round conversation.Round
	if err := env.DB.NewSelect().Model(&round).Where("conversation_id = ?", r1.ConversationID).Scan(ctx); err != nil {
		t.Fatalf("应写入服务轮次记录：%v", err)
	}
	if round.FirstReplyAt == nil || round.FirstReplyByID == "" || round.ClosedAt == nil {
		t.Fatalf("服务轮次记录应包含随同发出的首次回复与关闭信息：%+v", round)
	}

	// 会话 2：纯附带操作（不发消息）—— 空操作被拒；稍后处理 + 领取 + 加标签成功
	r2, err := recv.Handle(ctx, inAlpha, conversation.ReceiveParams{ChannelID: ch.ID, From: "u2", ExternalID: "m2", Text: "稍后处理我"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyAct.Handle(ctx, subAgent, r2.ConversationID, conversation.Actions{}); !errors.Is(err, conversation.ErrInvalidActions) {
		t.Fatalf("空操作应返回 ErrInvalidActions：%v", err)
	}
	until := time.Now().UTC().Add(3 * time.Hour)
	conv2, err := applyAct.Handle(ctx, subAgent, r2.ConversationID, conversation.Actions{
		Assignee:  &conversation.AssigneeChange{Mode: conversation.AssigneeMe},
		AddTagIDs: []string{refundTag.ID},
		Status:    &conversation.StatusAction{Kind: conversation.StatusActionSnooze, Until: &until},
	})
	if err != nil {
		t.Fatalf("纯附带操作失败：%v", err)
	}
	if conv2.Status != conversation.Snoozed || conv2.Assignee == nil || conv2.Assignee.ID != agentM.ID || len(conv2.Tags) != 1 {
		t.Fatalf("纯附带操作结果不符合预期：%+v", conv2)
	}
}
