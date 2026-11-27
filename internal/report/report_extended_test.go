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
