package report_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/report"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
	"agents-desk/internal/tickettype"
)

// TestExtendedReportsAggregationAndIsolation 覆盖概览、工单、商务沟通、跨业务成员人效与翻译场景拆解报表，
// 并校验跨工作区隔离及非法筛选 ID 返回 ErrNotFound。
func TestExtendedReportsAggregationAndIsolation(t *testing.T) {
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
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "beta", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	alphaSys := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	betaSys := identity.Subject{Type: identity.System, ID: "test", Tenant: beta.ID}

	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	agentA, err := addMember.Handle(ctx, alphaSys, member.AddParams{Email: "a@alpha.com", Name: "坐席A", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	team1, err := team.NewCreate(env.DB).Handle(ctx, alphaSys, team.Changes{Name: "技术支持", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	teamBeta, err := team.NewCreate(env.DB).Handle(ctx, betaSys, team.Changes{Name: "Beta团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	ttBug, err := tickettype.NewCreate(env.DB).Handle(ctx, alphaSys, tickettype.CreateParams{
		Name: "Bug 反馈", DefaultVisibility: tickettype.VisibilityAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	ttBeta, err := tickettype.NewCreate(env.DB).Handle(ctx, betaSys, tickettype.CreateParams{
		Name: "Beta类型", DefaultVisibility: tickettype.VisibilityMembers,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 查询窗口 2026-11-10..2026-11-12（3 天），上一对比期 2026-11-07..2026-11-09
	day1 := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	prevDay := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

	// 1. 创建工单：alpha 2 张主周期（1 已解决、1 处理中）+ 1 张对比期已解决；beta 1 张
	tkCreate := conversation.NewCreateTicket(env.DB, conversation.Downstream{})
	tk1, err := tkCreate.Handle(ctx, alphaSys, conversation.CreateTicketParams{
		Title: "支付超时", TypeID: &ttBug.ID, TeamID: &team1.ID, Visibility: tickettype.VisibilityAll, Priority: conversation.High,
	})
	if err != nil {
		t.Fatal(err)
	}
	tk2, err := tkCreate.Handle(ctx, alphaSys, conversation.CreateTicketParams{
		Title: "导出慢", TeamID: &team1.ID, Visibility: tickettype.VisibilityMembers, Priority: conversation.Normal,
	})
	if err != nil {
		t.Fatal(err)
	}
	tkPrev, err := tkCreate.Handle(ctx, alphaSys, conversation.CreateTicketParams{
		Title: "历史工单", TypeID: &ttBug.ID, TeamID: &team1.ID, Visibility: tickettype.VisibilityAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	tkBeta, err := tkCreate.Handle(ctx, betaSys, conversation.CreateTicketParams{
		Title: "Beta工单", TypeID: &ttBeta.ID, Visibility: tickettype.VisibilityAll,
	})
	if err != nil {
		t.Fatal(err)
	}

	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, status = 'resolved', assignee_type = 'member', assignee_id = ? WHERE id = ?", day1, agentA.ID, tk1.ID)
	mustExec(t, env.DB, "UPDATE tickets SET created_at = ?, resolved_at = ? WHERE conversation_id = ?", day1, day1.Add(3600*time.Second), tk1.ID)
	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, status = 'in_progress', assignee_type = 'member', assignee_id = ? WHERE id = ?", day2, agentA.ID, tk2.ID)
	mustExec(t, env.DB, "UPDATE tickets SET created_at = ? WHERE conversation_id = ?", day2, tk2.ID)
	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, status = 'resolved', assignee_type = 'member', assignee_id = ? WHERE id = ?", prevDay, agentA.ID, tkPrev.ID)
	mustExec(t, env.DB, "UPDATE tickets SET created_at = ?, resolved_at = ? WHERE conversation_id = ?", prevDay, prevDay.Add(1800*time.Second), tkPrev.ID)
	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, status = 'resolved' WHERE id = ?", day1, tkBeta.ID)
	mustExec(t, env.DB, "UPDATE tickets SET created_at = ?, resolved_at = ? WHERE conversation_id = ?", day1, day1.Add(600*time.Second), tkBeta.ID)

	// 2. 创建个人商务渠道与商务沟通会话（alpha 1 条 business_link 外部单聊 + 1 条内部聊天不计入商务；beta 1 条外部单聊）
	bizChan := &channel.Channel{
		ID:            uuid.Must(uuid.NewV7()).String(),
		TenantID:      alpha.ID,
		Kind:          channel.BusinessLink,
		Name:          "坐席A 商务链接",
		Settings:      map[string]any{},
		OwnerMemberID: &agentA.ID,
		Enabled:       true,
	}
	if _, err := env.DB.NewInsert().Model(bizChan).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	bizConvID := uuid.Must(uuid.NewV7()).String()
	mustExec(t, env.DB, `INSERT INTO conversations (id, tenant_id, kind, channel_id, status, priority, language, opened_at, last_message_at, last_seq, attributes, rating_comment, created_at, updated_at)
		VALUES (?, ?, 'chat', ?, 'open', 'normal', 'en', ?, ?, 2, '{}'::jsonb, '', ?, ?)`,
		bizConvID, alpha.ID, bizChan.ID, day1, day2, day1, day2)
	mustExec(t, env.DB, `INSERT INTO chats (conversation_id, tenant_id, mode, scope, name, channel_id, external_chat_id, created_at, updated_at)
		VALUES (?, ?, 'direct', 'external', '', ?, 'ext-1', ?, ?)`,
		bizConvID, alpha.ID, bizChan.ID, day1, day2)
	// 商务消息：1 条外部来信 + 1 条成员发出（最后一条为外部来信，用于验证 waiting_now）
	mustExec(t, env.DB, `INSERT INTO timeline_items (id, tenant_id, conversation_id, seq, kind, actor_type, actor_id, visibility, content, derived, created_at, updated_at)
		VALUES (?, ?, ?, 1, 'message', 'member', ?, 'all', '{"text":"hi partner"}'::jsonb, '{}'::jsonb, ?, ?),
		       (?, ?, ?, 2, 'message', 'contact', 'ext-c1', 'all', '{"text":"hello"}'::jsonb, '{}'::jsonb, ?, ?)`,
		uuid.Must(uuid.NewV7()).String(), alpha.ID, bizConvID, agentA.ID, day1.Add(time.Hour), day1.Add(time.Hour),
		uuid.Must(uuid.NewV7()).String(), alpha.ID, bizConvID, day2.Add(time.Hour), day2.Add(time.Hour))

	// 内部聊天（不应计入商务报表）
	intConvID := uuid.Must(uuid.NewV7()).String()
	mustExec(t, env.DB, `INSERT INTO conversations (id, tenant_id, kind, status, priority, language, opened_at, last_message_at, last_seq, attributes, rating_comment, created_at, updated_at)
		VALUES (?, ?, 'chat', 'open', 'normal', '', ?, ?, 1, '{}'::jsonb, '', ?, ?)`,
		intConvID, alpha.ID, day1, day1, day1, day1)
	mustExec(t, env.DB, `INSERT INTO chats (conversation_id, tenant_id, mode, scope, name, external_chat_id, created_at, updated_at)
		VALUES (?, ?, 'direct', 'internal', '', '', ?, ?)`,
		intConvID, alpha.ID, day1, day1)
	mustExec(t, env.DB, `INSERT INTO timeline_items (id, tenant_id, conversation_id, seq, kind, actor_type, actor_id, visibility, content, derived, created_at, updated_at)
		VALUES (?, ?, ?, 1, 'message', 'member', ?, 'members', '{"text":"internal"}'::jsonb, '{}'::jsonb, ?, ?)`,
		uuid.Must(uuid.NewV7()).String(), alpha.ID, intConvID, agentA.ID, day1, day1)

	// 3. 写入跨场景翻译调用记录（ticket + business + internal_chat）
	cost := 0.001
	for _, rec := range []*llm.Call{
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day1, UpdatedAt: day1, TenantID: &alpha.ID, CallType: llm.Translation, Operation: "incoming", TargetType: "conversation", TargetID: tk1.ID, Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 100, CompletionTokens: 50, Cost: &cost, DurationMS: 300},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day2, UpdatedAt: day2, TenantID: &alpha.ID, CallType: llm.Translation, Operation: "reply", TargetType: "conversation", TargetID: bizConvID, Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 200, CompletionTokens: 80, Cost: &cost, DurationMS: 400},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day2, UpdatedAt: day2, TenantID: &alpha.ID, CallType: llm.Translation, Operation: "fill", TargetType: "conversation", TargetID: intConvID, Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 60, CompletionTokens: 20, Cost: &cost, DurationMS: 200},
	} {
		if _, err := env.DB.NewInsert().Model(rec).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	p := period.Params{From: "2026-11-10", To: "2026-11-12", Granularity: period.Day, TimeZone: "UTC"}

	// 验证概览报表
	ov, err := report.NewOverview(env.DB, env.Roles).Handle(ctx, alphaSys, p)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Live.TicketOpen != 1 || ov.Live.TicketInProgress != 1 || ov.Live.BusinessWaiting != 1 {
		t.Fatalf("unexpected overview live: %+v", ov.Live)
	}
	if ov.Tickets.Created != 2 || ov.Tickets.Resolved != 1 || ov.Tickets.PrevResolved != 1 {
		t.Fatalf("unexpected overview tickets: %+v", ov.Tickets)
	}
	if ov.Business.NewChats != 1 || ov.Business.ActiveChats != 1 || ov.Business.MessagesTotal != 2 {
		t.Fatalf("unexpected overview business: %+v", ov.Business)
	}
	if ov.AI.Calls != 3 {
		t.Fatalf("unexpected overview ai: %+v", ov.AI)
	}
	if len(ov.Series) != 3 {
		t.Fatalf("expected 3 buckets in overview series, got %d", len(ov.Series))
	}

	// 验证工单报表与跨工作区隔离
	tkRep, err := report.NewTickets(env.DB, env.Roles, env.Files).Handle(ctx, alphaSys, report.TicketFilterParams{Params: p})
	if err != nil {
		t.Fatal(err)
	}
	if tkRep.Summary.Created != 2 || tkRep.Summary.Resolved != 1 || tkRep.Summary.InProgressNow != 1 {
		t.Fatalf("unexpected tickets summary: %+v", tkRep.Summary)
	}
	if tkRep.Summary.ResolutionMedianSec == nil || *tkRep.Summary.ResolutionMedianSec != 3600 {
		t.Fatalf("expected resolution median 3600, got %v", tkRep.Summary.ResolutionMedianSec)
	}
	if len(tkRep.ByType) != 2 || len(tkRep.ByAssignee) != 1 || tkRep.ByAssignee[0].Resolved != 1 {
		t.Fatalf("unexpected tickets breakdown: types=%+v assignees=%+v", tkRep.ByType, tkRep.ByAssignee)
	}
	if _, err := report.NewTickets(env.DB, env.Roles, env.Files).Handle(ctx, alphaSys, report.TicketFilterParams{Params: p, TeamID: teamBeta.ID}); !errors.Is(err, team.ErrNotFound) {
		t.Fatalf("expected team.ErrNotFound for cross-tenant team filter, got %v", err)
	}
	if _, err := report.NewTickets(env.DB, env.Roles, env.Files).Handle(ctx, alphaSys, report.TicketFilterParams{Params: p, TypeID: ttBeta.ID}); !errors.Is(err, tickettype.ErrNotFound) {
		t.Fatalf("expected tickettype.ErrNotFound for cross-tenant type filter, got %v", err)
	}

	// 验证商务沟通报表（内部聊天已被排除）
	bizRep, err := report.NewBusiness(env.DB, env.Roles, env.Files).Handle(ctx, alphaSys, report.BusinessFilterParams{Params: p})
	if err != nil {
		t.Fatal(err)
	}
	if bizRep.Summary.ActiveChats != 1 || bizRep.Summary.NewChats != 1 || bizRep.Summary.MessagesTotal != 2 || bizRep.Summary.MessagesInbound != 1 || bizRep.Summary.MessagesOutbound != 1 || bizRep.Summary.WaitingNow != 1 {
		t.Fatalf("unexpected business summary: %+v", bizRep.Summary)
	}
	if len(bizRep.ByChannelKind) != 1 || bizRep.ByChannelKind[0].Kind != channel.BusinessLink {
		t.Fatalf("unexpected business by_channel_kind: %+v", bizRep.ByChannelKind)
	}
	if len(bizRep.ByOwner) != 1 || bizRep.ByOwner[0].MemberID != agentA.ID || bizRep.ByOwner[0].MessagesOutbound != 1 {
		t.Fatalf("unexpected business by_owner: %+v", bizRep.ByOwner)
	}

	// 验证成员报表包含工单与商务产出
	memRep, err := report.NewMembers(env.DB, env.Roles, env.Files).Handle(ctx, alphaSys, report.FilterParams{Params: p})
	if err != nil {
		t.Fatal(err)
	}
	if memRep.Summary.TicketsResolved != 1 || memRep.Summary.BusinessMessages != 1 {
		t.Fatalf("unexpected members summary: %+v", memRep.Summary)
	}

	// 验证翻译报表按业务场景拆解
	trRep, err := report.NewTranslation(env.DB).Handle(ctx, alphaSys, p)
	if err != nil {
		t.Fatal(err)
	}
	sceneMap := map[string]int64{}
	for _, sc := range trRep.ByScene {
		sceneMap[sc.Scene] = sc.Calls
	}
	if sceneMap["ticket"] != 1 || sceneMap["business"] != 1 || sceneMap["internal_chat"] != 1 {
		t.Fatalf("unexpected translation by_scene: %+v", trRep.ByScene)
	}
}

// TestReportAudienceIsolationAndGroupCSAT 验证对外客服与内部服务在报表中的隔离、单聊/外部群拆分、
// 满意度参评率分母排除外部群与内部服务，以及翻译报表按 service / internal_service 拆分。
func TestReportAudienceIsolationAndGroupCSAT(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "iso", Slug: "iso", OwnerEmail: "owner@iso.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}

	custChan, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Audience: channel.AudienceCustomer, Name: "官网客服"})
	if err != nil {
		t.Fatal(err)
	}
	partnerChan, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Telegram, Audience: channel.AudiencePartner, Name: "合作方支持机器人"})
	if err != nil {
		t.Fatal(err)
	}
	intChan, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.WeCom, Audience: channel.AudienceInternal, Name: "IT 企微机器人"})
	if err != nil {
		t.Fatal(err)
	}

	day1 := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

	// 外部客户单聊（已关闭且已评价 5 星）+ 外部客户群 + 合作群 + 内部员工单聊/员工群
	custContactID := uuid.Must(uuid.NewV7()).String()
	intContactID := uuid.Must(uuid.NewV7()).String()
	custDirectID := uuid.Must(uuid.NewV7()).String()
	custGroupRefID := uuid.Must(uuid.NewV7()).String()
	custGroupConvID := uuid.Must(uuid.NewV7()).String()
	partnerGroupRefID := uuid.Must(uuid.NewV7()).String()
	partnerGroupConvID := uuid.Must(uuid.NewV7()).String()
	intDirectID := uuid.Must(uuid.NewV7()).String()
	intGroupRefID := uuid.Must(uuid.NewV7()).String()
	intGroupConvID := uuid.Must(uuid.NewV7()).String()

	mustExec(t, env.DB, `INSERT INTO contacts (id, tenant_id, number, kind, name, email, attributes, created_at, updated_at)
		VALUES (?, ?, 1, 'customer', '客户甲', '', '{}'::jsonb, ?, ?),
		       (?, ?, 2, 'internal', '内部员工乙', '', '{}'::jsonb, ?, ?)`,
		custContactID, tn.ID, day1, day1,
		intContactID, tn.ID, day1, day1)

	mustExec(t, env.DB, `INSERT INTO external_groups (id, tenant_id, channel_id, external_id, name, status, created_at, updated_at)
		VALUES (?, ?, ?, 'cg-1', 'VIP客户群', 'active', ?, ?),
		       (?, ?, ?, 'pg-1', '渠道合作群', 'active', ?, ?),
		       (?, ?, ?, 'ig-1', '研发IT应急群', 'active', ?, ?)`,
		custGroupRefID, tn.ID, custChan.ID, day1, day1,
		partnerGroupRefID, tn.ID, partnerChan.ID, day1, day1,
		intGroupRefID, tn.ID, intChan.ID, day1, day1)

	mustExec(t, env.DB, `INSERT INTO conversations (id, tenant_id, kind, audience, channel_id, contact_id, external_group_id, status, priority, language, opened_at, last_message_at, waiting_since, last_seq, attributes, rating_comment, created_at, updated_at)
		VALUES (?, ?, 'service', 'customer', ?, ?, NULL, 'closed', 'normal', 'zh-CN', ?, ?, NULL, 1, '{}'::jsonb, '', ?, ?),
		       (?, ?, 'service', 'customer', ?, NULL, ?, 'closed', 'normal', 'zh-CN', ?, ?, NULL, 1, '{}'::jsonb, '', ?, ?),
		       (?, ?, 'service', 'partner', ?, NULL, ?, 'open', 'normal', 'zh-CN', ?, ?, ?, 1, '{}'::jsonb, '', ?, ?),
		       (?, ?, 'service', 'internal', ?, ?, NULL, 'open', 'normal', 'zh-CN', ?, ?, ?, 1, '{}'::jsonb, '', ?, ?),
		       (?, ?, 'service', 'internal', ?, NULL, ?, 'closed', 'normal', 'zh-CN', ?, ?, NULL, 1, '{}'::jsonb, '', ?, ?)`,
		custDirectID, tn.ID, custChan.ID, custContactID, day1, day1, day1, day1,
		custGroupConvID, tn.ID, custChan.ID, custGroupRefID, day1, day1, day1, day1,
		partnerGroupConvID, tn.ID, partnerChan.ID, partnerGroupRefID, day1, day1, day1, day1, day1,
		intDirectID, tn.ID, intChan.ID, intContactID, day1, day1, day1, day1, day1,
		intGroupConvID, tn.ID, intChan.ID, intGroupRefID, day1, day1, day1, day1)

	// 写入轮次：
	// - custDirect: 首响 60s，关闭 300s，5 星评价
	// - custGroup: 首响 120s，关闭 400s，无评价（外部群不计入参评率分母）
	// - partnerGroup: 首响 45s，未关闭（独立归入 partner 服务报表）
	// - intGroup: 首响 30s，关闭 180s，无评价（内部服务不计入客服报表与参评率）
	score5 := 5
	for _, r := range []*conversation.Round{
		{
			ID: uuid.Must(uuid.NewV7()).String(), TenantID: tn.ID, ConversationID: custDirectID, Round: 1,
			OpenedAt: day1, FirstReplyAt: ptrTime(day1.Add(60 * time.Second)), ClosedAt: ptrTime(day1.Add(300 * time.Second)),
			Rating: &score5, RatedAt: ptrTime(day1.Add(360 * time.Second)),
		},
		{
			ID: uuid.Must(uuid.NewV7()).String(), TenantID: tn.ID, ConversationID: custGroupConvID, Round: 1,
			OpenedAt: day1, FirstReplyAt: ptrTime(day1.Add(120 * time.Second)), ClosedAt: ptrTime(day1.Add(400 * time.Second)),
		},
		{
			ID: uuid.Must(uuid.NewV7()).String(), TenantID: tn.ID, ConversationID: partnerGroupConvID, Round: 1,
			OpenedAt: day1, FirstReplyAt: ptrTime(day1.Add(45 * time.Second)),
		},
		{
			ID: uuid.Must(uuid.NewV7()).String(), TenantID: tn.ID, ConversationID: intGroupConvID, Round: 1,
			OpenedAt: day1, FirstReplyAt: ptrTime(day1.Add(30 * time.Second)), ClosedAt: ptrTime(day1.Add(180 * time.Second)),
		},
	} {
		if _, err := env.DB.NewInsert().Model(r).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 翻译调用：1 条对外客服 + 1 条合作方服务 + 1 条内部服务
	cost := 0.001
	for _, rec := range []*llm.Call{
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day1, UpdatedAt: day1, TenantID: &tn.ID, CallType: llm.Translation, Operation: "incoming", TargetType: "conversation", TargetID: custDirectID, Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 50, CompletionTokens: 20, Cost: &cost, DurationMS: 100},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day1, UpdatedAt: day1, TenantID: &tn.ID, CallType: llm.Translation, Operation: "incoming", TargetType: "conversation", TargetID: partnerGroupConvID, Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 50, CompletionTokens: 20, Cost: &cost, DurationMS: 100},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day1, UpdatedAt: day1, TenantID: &tn.ID, CallType: llm.Translation, Operation: "incoming", TargetType: "conversation", TargetID: intDirectID, Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 50, CompletionTokens: 20, Cost: &cost, DurationMS: 100},
	} {
		if _, err := env.DB.NewInsert().Model(rec).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	p := period.Params{From: "2026-11-10", To: "2026-11-10", Granularity: period.Day, TimeZone: "UTC"}

	// 1. 会话报表按 audience 三域隔离与单聊/外部群拆分
	custConv, err := report.NewConversations(env.DB).Handle(ctx, sys, report.FilterParams{Params: p, Audience: channel.AudienceCustomer})
	if err != nil {
		t.Fatal(err)
	}
	if custConv.Summary.Created != 2 || custConv.Summary.DirectCreated != 1 || custConv.Summary.GroupCreated != 1 || len(custConv.ByChannel) != 1 || custConv.ByChannel[0].ID != custChan.ID {
		t.Fatalf("unexpected customer conversations: sum=%+v by_channel=%+v", custConv.Summary, custConv.ByChannel)
	}

	partnerConv, err := report.NewConversations(env.DB).Handle(ctx, sys, report.FilterParams{Params: p, Audience: channel.AudiencePartner})
	if err != nil {
		t.Fatal(err)
	}
	if partnerConv.Summary.Created != 1 || partnerConv.Summary.DirectCreated != 0 || partnerConv.Summary.GroupCreated != 1 || partnerConv.Summary.OpenNow != 1 || len(partnerConv.ByChannel) != 1 || partnerConv.ByChannel[0].ID != partnerChan.ID {
		t.Fatalf("unexpected partner conversations: sum=%+v by_channel=%+v", partnerConv.Summary, partnerConv.ByChannel)
	}

	intConv, err := report.NewConversations(env.DB).Handle(ctx, sys, report.FilterParams{Params: p, Audience: channel.AudienceInternal})
	if err != nil {
		t.Fatal(err)
	}
	if intConv.Summary.Created != 2 || intConv.Summary.DirectCreated != 1 || intConv.Summary.GroupCreated != 1 || intConv.Summary.OpenNow != 1 || len(intConv.ByChannel) != 1 || intConv.ByChannel[0].ID != intChan.ID {
		t.Fatalf("unexpected internal conversations: sum=%+v by_channel=%+v", intConv.Summary, intConv.ByChannel)
	}

	// 2. 响应时间按 audience=partner 与 audience=internal 严格隔离
	partnerRT, err := report.NewResponseTime(env.DB).Handle(ctx, sys, report.FilterParams{Params: p, Audience: channel.AudiencePartner})
	if err != nil {
		t.Fatal(err)
	}
	if partnerRT.FirstReply.Samples != 1 || partnerRT.FirstReply.MedianSeconds == nil || *partnerRT.FirstReply.MedianSeconds != 45 || len(partnerRT.ByChannel) != 1 || partnerRT.ByChannel[0].ID != partnerChan.ID {
		t.Fatalf("unexpected partner response time: frt=%+v by_channel=%+v", partnerRT.FirstReply, partnerRT.ByChannel)
	}

	intRT, err := report.NewResponseTime(env.DB).Handle(ctx, sys, report.FilterParams{Params: p, Audience: channel.AudienceInternal})
	if err != nil {
		t.Fatal(err)
	}
	if intRT.FirstReply.Samples != 1 || intRT.FirstReply.MedianSeconds == nil || *intRT.FirstReply.MedianSeconds != 30 || len(intRT.ByChannel) != 1 || intRT.ByChannel[0].ID != intChan.ID {
		t.Fatalf("unexpected internal response time: frt=%+v by_channel=%+v", intRT.FirstReply, intRT.ByChannel)
	}

	// 3. 满意度参评率：仅 1 个可评价的外部客户单聊关闭轮次且已评 5 星 → ClosedCount=1, ResponseRate=100%
	sat, err := report.NewSatisfaction(env.DB).Handle(ctx, sys, report.FilterParams{Params: p})
	if err != nil {
		t.Fatal(err)
	}
	if sat.Summary.RatedCount != 1 || sat.Summary.ClosedCount != 1 || sat.Summary.ResponseRate == nil || *sat.Summary.ResponseRate != 100 {
		t.Fatalf("unexpected satisfaction summary: %+v", sat.Summary)
	}

	// 4. 概览报表三域隔离（customer / partner / internal）
	ov, err := report.NewOverview(env.DB, env.Roles).Handle(ctx, sys, p)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Live.ServiceOpen != 0 || ov.Live.PartnerOpen != 1 || ov.Live.PartnerWaiting != 1 || ov.Live.InternalOpen != 1 || ov.Live.InternalWaiting != 1 {
		t.Fatalf("unexpected overview live: %+v", ov.Live)
	}
	if ov.Service.Created != 2 || ov.Partner.Created != 1 || ov.Partner.GroupCreated != 1 || ov.Internal.Created != 2 || ov.Internal.GroupCreated != 1 || ov.Internal.Closed != 1 {
		t.Fatalf("unexpected overview service=%+v partner=%+v internal=%+v", ov.Service, ov.Partner, ov.Internal)
	}
	if len(ov.Series) != 1 || ov.Series[0].ServiceCreated != 2 || ov.Series[0].PartnerCreated != 1 || ov.Series[0].InternalCreated != 2 {
		t.Fatalf("unexpected overview series: %+v", ov.Series)
	}

	// 5. 翻译报表区分 service、partner_service 与 internal_service
	tr, err := report.NewTranslation(env.DB).Handle(ctx, sys, p)
	if err != nil {
		t.Fatal(err)
	}
	scenes := map[string]int64{}
	for _, sc := range tr.ByScene {
		scenes[sc.Scene] = sc.Calls
	}
	if scenes["service"] != 1 || scenes["partner_service"] != 1 || scenes["internal_service"] != 1 {
		t.Fatalf("unexpected translation scenes: %+v", tr.ByScene)
	}

	// 6. 商务外部群（external_user）来信/待跟进、ByLanguage 在期末之后仍有新消息时保留，以及 timeline_item/ticket 翻译场景归类
	bizGroupConvID := uuid.Must(uuid.NewV7()).String()
	later := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	mustExec(t, env.DB, `INSERT INTO conversations (id, tenant_id, kind, channel_id, status, priority, language, opened_at, last_message_at, last_seq, attributes, rating_comment, created_at, updated_at)
		VALUES (?, ?, 'chat', NULL, 'open', 'normal', 'ja', ?, ?, 2, '{}'::jsonb, '', ?, ?)`,
		bizGroupConvID, tn.ID, day1, later, day1, later)
	mustExec(t, env.DB, `INSERT INTO chats (conversation_id, tenant_id, mode, scope, name, external_chat_id, created_at, updated_at)
		VALUES (?, ?, 'group', 'external', 'Partner Group', 'pg-chat-1', ?, ?)`,
		bizGroupConvID, tn.ID, day1, later)
	extMsgID := uuid.Must(uuid.NewV7()).String()
	laterMsgID := uuid.Must(uuid.NewV7()).String()
	mustExec(t, env.DB, `INSERT INTO timeline_items (id, tenant_id, conversation_id, seq, kind, actor_type, actor_id, visibility, content, derived, created_at, updated_at)
		VALUES (?, ?, ?, 1, 'message', 'external_user', 'tg-u-9', 'all', '{"text":"こんにちは"}'::jsonb, '{"language":"ja"}'::jsonb, ?, ?),
		       (?, ?, ?, 2, 'message', 'external_user', 'tg-u-9', 'all', '{"text":"追加"}'::jsonb, '{"language":"ja"}'::jsonb, ?, ?)`,
		extMsgID, tn.ID, bizGroupConvID, day1, day1,
		laterMsgID, tn.ID, bizGroupConvID, later, later)
	for _, rec := range []*llm.Call{
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day1, UpdatedAt: day1, TenantID: &tn.ID, CallType: llm.Translation, Operation: "incoming", TargetType: "timeline_item", TargetID: extMsgID, Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 10, CompletionTokens: 5, Cost: &cost, DurationMS: 50},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day1, UpdatedAt: day1, TenantID: &tn.ID, CallType: llm.Translation, Operation: "ticket_meta", TargetType: "ticket", TargetID: uuid.Must(uuid.NewV7()).String(), Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 10, CompletionTokens: 5, Cost: &cost, DurationMS: 50},
	} {
		if _, err := env.DB.NewInsert().Model(rec).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	bizGroupRep, err := report.NewBusiness(env.DB, env.Roles, env.Files).Handle(ctx, sys, report.BusinessFilterParams{Params: p})
	if err != nil {
		t.Fatal(err)
	}
	if bizGroupRep.Summary.WaitingNow != 1 || bizGroupRep.Summary.MessagesInbound != 1 || len(bizGroupRep.ByLanguage) != 1 || bizGroupRep.ByLanguage[0].Language != "ja" {
		t.Fatalf("external_user business metrics mismatch: sum=%+v by_lang=%+v", bizGroupRep.Summary, bizGroupRep.ByLanguage)
	}
	ovAfter, err := report.NewOverview(env.DB, env.Roles).Handle(ctx, sys, p)
	if err != nil {
		t.Fatal(err)
	}
	if ovAfter.Live.BusinessWaiting != 1 {
		t.Fatalf("expected overview BusinessWaiting=1 for external_user, got %d", ovAfter.Live.BusinessWaiting)
	}
	trAfter, err := report.NewTranslation(env.DB).Handle(ctx, sys, p)
	if err != nil {
		t.Fatal(err)
	}
	scenesAfter := map[string]int64{}
	for _, sc := range trAfter.ByScene {
		scenesAfter[sc.Scene] = sc.Calls
	}
	if scenesAfter["business"] != 1 || scenesAfter["ticket"] != 1 {
		t.Fatalf("unexpected translation scenes with timeline_item/ticket: %+v", trAfter.ByScene)
	}
	foundJa := false
	for _, lc := range trAfter.DetectedLanguages {
		if lc.Language == "ja" && lc.Count == 1 {
			foundJa = true
		}
	}
	if !foundJa {
		t.Fatalf("expected ja detected language from external_user: %+v", trAfter.DetectedLanguages)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
