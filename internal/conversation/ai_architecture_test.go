package conversation_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

func insertTestBot(t *testing.T, env *apptest.Env, tenantID, name string, enabled bool) string {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	at := time.Now().UTC()
	if _, err := env.DB.NewRaw(`INSERT INTO bots (id, created_at, updated_at, tenant_id, name, enabled, settings)
		VALUES (?, ?, ?, ?, ?, ?, '{}')`, id, at, at, tenantID, name, enabled).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertActiveGeneration(t *testing.T, env *apptest.Env, tenantID, convID, botID, triggerItemID string) string {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	at := time.Now().UTC()
	if _, err := env.DB.NewRaw(`INSERT INTO generations (id, created_at, updated_at, tenant_id, conversation_id, actor_id, trigger_item_id, state, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'streaming', ?)`,
		id, at, at, tenantID, convID, botID, triggerItemID, at.Add(5*time.Minute)).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestBotFirstClassAssignmentAndHandlingStateMachine 验证：
// 1. BotActor 指派校验、名称回填与普通坐席直接接管；
// 2. 成员回复自动将 ai_auto 转为 ai_paused 并触发活跃生成取消回调；
// 3. 筛选 assignee_type=bot、handling_mode 与 InboxDTO.WaitingApproval；
// 4. BotActor 消息可翻译且不改变客户语言，内部单聊支持启用 Bot。
func TestBotFirstClassAssignmentAndHandlingStateMachine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "BetaOwner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	owner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	agentMember, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, sys, member.AddParams{
		Email: "agent@alpha.com", Name: "坐席甲", RoleID: agentRole.ID, Password: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	agentSub := identity.Subject{Type: member.Type, ID: agentMember.ID, Tenant: tn.ID, Permissions: auth.WithBase(agentRole.Permissions)}
	betaSub := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}

	tm, err := team.NewCreate(env.DB).Handle(ctx, owner, team.Changes{Name: "客服一组"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := team.NewPutMember(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, owner, tm.ID, agentMember.ID); err != nil {
		t.Fatal(err)
	}

	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道", TeamID: &tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	otherCh, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "其他未分配渠道"})
	if err != nil {
		t.Fatal(err)
	}

	var canceledConvs []string
	down := conversation.Downstream{
		OnGenerationCanceled: func(_ context.Context, _ string, convID string) {
			canceledConvs = append(canceledConvs, convID)
		},
	}

	rec, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c1", FromName: "客户C1", Text: "你好",
	})
	if err != nil {
		t.Fatal(err)
	}

	activeBot := insertTestBot(t, env, tn.ID, "小智助手", true)
	disabledBot := insertTestBot(t, env, tn.ID, "已停用机器人", false)
	betaBot := insertTestBot(t, env, beta.ID, "跨租户机器人", true)

	assign := conversation.NewAssign(env.DB, down)
	botType := conversation.BotActor

	// 停用与跨租户机器人禁止指派
	if _, err := assign.Handle(ctx, owner, rec.ConversationID, conversation.Assignment{AssigneeType: &botType, AssigneeID: &disabledBot}); !errors.Is(err, conversation.ErrAssigneeBotNotFound) {
		t.Fatalf("停用机器人应拒绝指派: %v", err)
	}
	if _, err := assign.Handle(ctx, owner, rec.ConversationID, conversation.Assignment{AssigneeType: &botType, AssigneeID: &betaBot}); !errors.Is(err, conversation.ErrAssigneeBotNotFound) {
		t.Fatalf("跨租户机器人应拒绝指派: %v", err)
	}

	// 指派给启用的机器人并验证 DTO 与列表筛选
	dto, err := assign.Handle(ctx, owner, rec.ConversationID, conversation.Assignment{AssigneeType: &botType, AssigneeID: &activeBot})
	if err != nil {
		t.Fatal(err)
	}
	if dto.Assignee == nil || dto.Assignee.Type != conversation.BotActor || dto.Assignee.ID != activeBot || dto.Assignee.Name != "小智助手" {
		t.Fatalf("指派 Bot 返回异常: %+v", dto.Assignee)
	}
	if dto.HandlingMode != conversation.HandlingAIAuto {
		t.Fatalf("指派 Bot 后应自动进入 ai_auto 模式: %s", dto.HandlingMode)
	}

	botFilterRes, err := conversation.NewList(env.DB).Handle(ctx, owner, &conversation.ListInput{
		AssigneeType: conversation.BotActor,
		AssigneeID:   activeBot,
		HandlingMode: conversation.HandlingAIAuto,
	})
	if err != nil || len(botFilterRes.Items) != 1 || botFilterRes.Items[0].ID != rec.ConversationID {
		t.Fatalf("按 Bot 与 HandlingMode 筛选应命中会话: len=%d err=%v", len(botFilterRes.Items), err)
	}

	// 普通坐席（无 conversations:assign）可以直接领取本团队 Bot 负责的会话
	claimed, err := assign.Handle(ctx, agentSub, rec.ConversationID, conversation.Assignment{AssigneeID: &agentMember.ID})
	if err != nil {
		t.Fatalf("普通坐席领取 Bot 会话应允许: %v", err)
	}
	if claimed.Assignee == nil || claimed.Assignee.Type != conversation.MemberActor || claimed.Assignee.ID != agentMember.ID || claimed.HandlingMode != conversation.HandlingHuman {
		t.Fatalf("坐席接管后负责人与模式异常: %+v mode=%s", claimed.Assignee, claimed.HandlingMode)
	}

	// 不在坐席所属团队的 Bot 会话仍受 ScopeTeam 可见性约束，坐席不可越权访问
	otherRec, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: otherCh.ID, From: "c-other", FromName: "其他客户", Text: "不在同一团队",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, owner, otherRec.ConversationID, conversation.Assignment{AssigneeType: &botType, AssigneeID: &activeBot}); err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, agentSub, otherRec.ConversationID, conversation.Assignment{AssigneeID: &agentMember.ID}); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("ScopeTeam 坐席不应能跨团队领取 Bot 会话: %v", err)
	}

	// 切换回 ai_auto 并插入一条活跃生成，验证等待审批统计与成员回复自动暂停 + 取消生成
	setMode := conversation.NewSetHandlingMode(env.DB, down)
	if _, err := setMode.Handle(ctx, betaSub, rec.ConversationID, conversation.HandlingModeBody{Mode: conversation.HandlingAIAuto, BotID: activeBot}); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("其他工作区成员应不可切换接待模式: %v", err)
	}
	if _, err := setMode.Handle(ctx, owner, rec.ConversationID, conversation.HandlingModeBody{Mode: conversation.HandlingAIAuto, BotID: activeBot}); err != nil {
		t.Fatal(err)
	}
	if _, err := setMode.Handle(ctx, owner, rec.ConversationID, conversation.HandlingModeBody{Mode: conversation.HandlingWaitingApproval}); !errors.Is(err, conversation.ErrInvalidHandlingMode) {
		t.Fatalf("成员不可手动切为 waiting_approval: %v", err)
	}

	// 模拟进入 waiting_approval，验证 InboxDTO.WaitingApproval
	if _, err := env.DB.NewRaw(`UPDATE conversations SET handling_mode = 'waiting_approval' WHERE id = ?`, rec.ConversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	inbox, err := conversation.NewGetInbox(env.DB).Handle(ctx, owner)
	if err != nil || inbox.WaitingApproval != 1 {
		t.Fatalf("Inbox WaitingApproval 应为 1: got=%d err=%v", inbox.WaitingApproval, err)
	}

	genID := insertActiveGeneration(t, env, tn.ID, rec.ConversationID, activeBot, rec.Item.ID)

	// 成员回复带 SourceGenerationID，校验跨会话越权并验证同会话暂停 AI + 取消活跃生成
	if _, err := conversation.NewReply(env.DB, down).Handle(ctx, agentSub, rec.ConversationID, conversation.ReplyBody{
		Text: "无效引用", SourceGenerationID: uuid.Must(uuid.NewV7()).String(),
	}); !errors.Is(err, conversation.ErrGenerationNotFound) {
		t.Fatalf("不存在的 source_generation_id 应报 ErrGenerationNotFound: %v", err)
	}

	canceledConvs = nil
	replyDTO, err := conversation.NewReply(env.DB, down).Handle(ctx, agentSub, rec.ConversationID, conversation.ReplyBody{
		Text: "人工来为您处理", SourceGenerationID: genID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(canceledConvs) != 1 || canceledConvs[0] != rec.ConversationID {
		t.Fatalf("成员回复应触发 OnGenerationCanceled 回调: %+v", canceledConvs)
	}
	var genState, genReason string
	if err := env.DB.NewRaw(`SELECT state, cancel_reason FROM generations WHERE id = ?`, genID).Scan(ctx, &genState, &genReason); err != nil {
		t.Fatal(err)
	}
	if genState != "canceled" || genReason != "human_takeover" {
		t.Fatalf("生成应被取消并记录原因: state=%s reason=%s", genState, genReason)
	}
	afterReply, err := conversation.NewGet(env.DB).Handle(ctx, agentSub, rec.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReply.HandlingMode != conversation.HandlingAIPaused || afterReply.Assignee == nil || afterReply.Assignee.Type != conversation.MemberActor || afterReply.Assignee.ID != agentMember.ID {
		t.Fatalf("人工回复后应变为 ai_paused 并归属介入坐席: mode=%s assignee=%+v reply=%s", afterReply.HandlingMode, afterReply.Assignee, replyDTO.ID)
	}

	// BotActor 消息可翻译，且 SaveTranslation 不篡改客户语言
	botItem := &conversation.Item{
		Kind: conversation.Message, ActorType: conversation.BotActor, ActorID: activeBot, Visibility: conversation.Everyone,
		Content: conversation.Content{Text: "Your order has shipped."},
	}
	if !botItem.Translatable() {
		t.Fatalf("BotActor 消息应支持翻译给坐席阅读")
	}
	if _, err := env.DB.NewRaw(`UPDATE conversations SET language = 'zh-CN' WHERE id = ?`, rec.ConversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`UPDATE contacts SET language = 'zh-CN' WHERE tenant_id = ?`, tn.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var botItemID string
	botAt := time.Now().UTC()
	botItemID = uuid.Must(uuid.NewV7()).String()
	if _, err := env.DB.NewRaw(`INSERT INTO timeline_items (id, created_at, updated_at, tenant_id, conversation_id, seq, kind, actor_type, actor_id, visibility, content, derived)
		VALUES (?, ?, ?, ?, ?, 99, 'message', 'bot', ?, 'all', '{"text":"Your order has shipped."}', '{}')`,
		botItemID, botAt, botAt, tn.ID, rec.ConversationID, activeBot).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := conversation.SaveTranslation(ctx, env.DB, down, tn.ID, botItemID, conversation.Translated{
		Language:     "en",
		Translations: map[string]string{"zh-CN": "您的订单已发货。"},
	}); err != nil {
		t.Fatal(err)
	}
	var convLang string
	if err := env.DB.NewRaw(`SELECT language FROM conversations WHERE id = ?`, rec.ConversationID).Scan(ctx, &convLang); err != nil || convLang != "zh-CN" {
		t.Fatalf("Bot 消息翻译不应篡改客户语言: got=%q err=%v", convLang, err)
	}

	// 内部单聊支持启用 BotActor 并填充 Bot 名称
	dmBot, err := conversation.NewCreateDirectChat(env.DB, down).Handle(ctx, owner, conversation.CreateDirectChatParams{
		ParticipantType: conversation.BotActor, ParticipantID: activeBot,
	})
	if err != nil {
		t.Fatalf("与启用 Bot 发起单聊失败: %v", err)
	}
	if dmBot.Peer == nil || dmBot.Peer.Type != conversation.BotActor || dmBot.Peer.ID != activeBot || dmBot.Peer.Name != "小智助手" {
		t.Fatalf("Bot 单聊对方信息异常: %+v", dmBot.Peer)
	}
}

// TestInboundRoutingRoundResolutionAndMerge 验证渠道 ai_reception 路由、关闭轮次解决归因与合并会话取消活跃生成。
func TestInboundRoutingRoundResolutionAndMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Gamma", Slug: "gamma", OwnerEmail: "owner@gamma.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	owner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	tm, err := team.NewCreate(env.DB).Handle(ctx, owner, team.Changes{Name: "AI 兜底团队"})
	if err != nil {
		t.Fatal(err)
	}
	botID := insertTestBot(t, env, tn.ID, "接待专员", true)

	chAuto, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{
		Kind: channel.Test, Name: "自动接待渠道", TeamID: &tm.ID,
		Settings: map[string]any{"ai_reception": map[string]any{"enabled": true, "bot_id": botID, "mode": "ai_auto"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	chCopilot, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{
		Kind: channel.Test, Name: "副驾驶渠道", TeamID: &tm.ID,
		Settings: map[string]any{"ai_reception": map[string]any{"enabled": true, "bot_id": botID, "mode": "ai_copilot"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	down := conversation.Downstream{}
	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)

	// 1. ai_auto 渠道：自动指派 BotActor 且保留团队，发 Bot 回复后关闭应记为 ai_resolved
	r1, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: chAuto.ID, From: "u1", FromName: "U1", Text: "查订单"})
	if err != nil {
		t.Fatal(err)
	}
	c1, err := conversation.NewGet(env.DB).Handle(ctx, owner, r1.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if c1.HandlingMode != conversation.HandlingAIAuto || c1.Assignee == nil || c1.Assignee.Type != conversation.BotActor || c1.TeamID == nil || *c1.TeamID != tm.ID {
		t.Fatalf("ai_auto 路由异常: mode=%s assignee=%+v team=%v", c1.HandlingMode, c1.Assignee, c1.TeamID)
	}
	_ = insertActiveGeneration(t, env, tn.ID, c1.ID, botID, r1.Item.ID)
	if _, err := env.DB.NewRaw(`UPDATE generations SET state = 'completed', item_id = ? WHERE conversation_id = ?`, r1.Item.ID, c1.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO timeline_items (id, created_at, updated_at, tenant_id, conversation_id, seq, kind, actor_type, actor_id, visibility, content, derived)
		VALUES (?, ?, ?, ?, ?, 10, 'message', 'bot', ?, 'all', '{"text":"已发货"}', '{}')`,
		uuid.Must(uuid.NewV7()).String(), time.Now().UTC(), time.Now().UTC(), tn.ID, c1.ID, botID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewClose(env.DB, down).Handle(ctx, owner, c1.ID); err != nil {
		t.Fatal(err)
	}
	rounds1, err := conversation.NewListRounds(env.DB).Handle(ctx, owner, c1.ID)
	if err != nil || len(rounds1) != 1 || rounds1[0].ResolutionType != conversation.ResolutionAIResolved || rounds1[0].AIGenerationCount != 1 {
		t.Fatalf("纯 Bot 回复关闭应记 ai_resolved: %+v err=%v", rounds1, err)
	}

	// 2. ai_copilot 渠道：不指派 Bot、成员回复后关闭应记为 ai_assisted
	r2, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: chCopilot.ID, From: "u2", FromName: "U2", Text: "需要协助"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := conversation.NewGet(env.DB).Handle(ctx, owner, r2.ConversationID)
	if err != nil || c2.HandlingMode != conversation.HandlingAICopilot || c2.Assignee != nil {
		t.Fatalf("ai_copilot 路由异常: %+v err=%v", c2, err)
	}
	_ = insertActiveGeneration(t, env, tn.ID, c2.ID, botID, r2.Item.ID)
	if _, err := conversation.NewReply(env.DB, down).Handle(ctx, owner, c2.ID, conversation.ReplyBody{Text: "人工回复"}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewClose(env.DB, down).Handle(ctx, owner, c2.ID); err != nil {
		t.Fatal(err)
	}
	rounds2, err := conversation.NewListRounds(env.DB).Handle(ctx, owner, c2.ID)
	if err != nil || len(rounds2) != 1 || rounds2[0].ResolutionType != conversation.ResolutionAIAssisted || rounds2[0].AIGenerationCount != 1 {
		t.Fatalf("有 AI 生成且人工回复关闭应记 ai_assisted: %+v err=%v", rounds2, err)
	}

	// 3. 两个会话都有活跃生成时合并联系人，源会话活跃生成先取消，不撞单活唯一索引
	r3, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: chAuto.ID, From: "u3", FromName: "U3", Text: "目标"})
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "第二渠道"})
	if err != nil {
		t.Fatal(err)
	}
	r4, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch2.ID, From: "u4", FromName: "U4", Text: "来源"})
	if err != nil {
		t.Fatal(err)
	}
	_ = insertActiveGeneration(t, env, tn.ID, r3.ConversationID, botID, r3.Item.ID)
	srcGenID := insertActiveGeneration(t, env, tn.ID, r4.ConversationID, botID, r4.Item.ID)
	turnID := uuid.Must(uuid.NewV7()).String()
	turnAt := time.Now().UTC()
	if _, err := env.DB.NewRaw(`INSERT INTO agent_turns (id, created_at, updated_at, tenant_id, conversation_id, generation_id, after_seq, turn_order, role, message)
		VALUES (?, ?, ?, ?, ?, ?, 1, 1, 'assistant', '{"role":"assistant"}')`,
		turnID, turnAt, turnAt, tn.ID, r4.ConversationID, srcGenID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	var targetCtID, sourceCtID string
	if err := env.DB.NewRaw(`SELECT contact_id::text FROM conversations WHERE id = ?`, r3.ConversationID).Scan(ctx, &targetCtID); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.NewRaw(`SELECT contact_id::text FROM conversations WHERE id = ?`, r4.ConversationID).Scan(ctx, &sourceCtID); err != nil {
		t.Fatal(err)
	}
	if _, err := contact.NewMerge(env.DB, conversation.OnContactMerge(down)).Handle(ctx, owner, targetCtID, contact.MergeBody{SourceID: sourceCtID}); err != nil {
		t.Fatalf("双活跃生成合并应先取消源生成而非撞单活索引: %v", err)
	}
	var srcState, srcReason, mergedConvID string
	if err := env.DB.NewRaw(`SELECT state, cancel_reason, conversation_id::text FROM generations WHERE id = ?`, srcGenID).Scan(ctx, &srcState, &srcReason, &mergedConvID); err != nil {
		t.Fatal(err)
	}
	if srcState != "canceled" || srcReason != "merged" || mergedConvID != r3.ConversationID {
		t.Fatalf("源生成应先取消再迁移到目标会话: state=%s reason=%s conv=%s", srcState, srcReason, mergedConvID)
	}
	var turnConvID string
	if err := env.DB.NewRaw(`SELECT conversation_id::text FROM agent_turns WHERE id = ?`, turnID).Scan(ctx, &turnConvID); err != nil || turnConvID != r3.ConversationID {
		t.Fatalf("agent_turns 应同步迁移到目标会话: got=%s err=%v", turnConvID, err)
	}
}
