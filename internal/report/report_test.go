package report_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/report"
	"agents-desk/internal/tag"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// TestReportsAggregationAndIsolation 覆盖全部 5 张基础报表与评价分页明细：
//   - 会话量、重开、积压快照、趋势补零与渠道/团队/标签/语言分布；
//   - 首次回复时长（FRT）与解决时长的中位数/均值/P90/直方图/对比期；
//   - 满意度 CSAT（4–5 星占比）、平均分、星级分布、参评率与星级筛选分页；
//   - 成员绩效：A 首响后转派 B 关闭时，FRT 准确归因给 A、关闭与满意度归因给 B，且保留已移出成员与已停用团队历史；
//   - 翻译报表：按 operation/model 统计调用量、成功率、token、费用与语言分布；
//   - 跨工作区隔离：另一工作区数据绝不泄入当前工作区报表，跨工作区团队/渠道筛选返回 ErrNotFound。
func TestReportsAggregationAndIsolation(t *testing.T) {
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

	// 在 alpha 中添加两位坐席：agentA（随后移出工作区验证历史保留）和 agentB（保留在职）
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	agentA, err := addMember.Handle(ctx, alphaSys, member.AddParams{Email: "a@alpha.com", Name: "坐席A", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	agentB, err := addMember.Handle(ctx, alphaSys, member.AddParams{Email: "b@alpha.com", Name: "坐席B", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}

	// 团队：team1（正常）、team2（随后停用，验证报表仍保留其历史）
	team1, err := team.NewCreate(env.DB).Handle(ctx, alphaSys, team.Changes{Name: "一线客服", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	team2, err := team.NewCreate(env.DB).Handle(ctx, alphaSys, team.Changes{Name: "旧团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	chAlpha, err := channel.NewCreate(env.DB).Handle(ctx, alphaSys, channel.CreateParams{Kind: channel.Test, Name: "官网", TeamID: &team1.ID})
	if err != nil {
		t.Fatal(err)
	}
	chBeta, err := channel.NewCreate(env.DB).Handle(ctx, betaSys, channel.CreateParams{Kind: channel.Test, Name: "Beta渠道"})
	if err != nil {
		t.Fatal(err)
	}
	vipTag, err := tag.NewCreate(env.DB).Handle(ctx, alphaSys, tag.Changes{Name: "VIP", Color: tag.Yellow})
	if err != nil {
		t.Fatal(err)
	}

	// 构造确定性日期（UTC）：查询窗口 2026-11-10..2026-11-12（3 天），上一对比期 2026-11-07..2026-11-09
	day1 := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	prevDay := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{})
	c1, err := recv.Handle(ctx, alphaSys, conversation.ReceiveParams{ChannelID: chAlpha.ID, From: "u1", FromName: "Alice", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := recv.Handle(ctx, alphaSys, conversation.ReceiveParams{ChannelID: chAlpha.ID, From: "u2", FromName: "Bob", Text: "namaste"})
	if err != nil {
		t.Fatal(err)
	}
	cPrev, err := recv.Handle(ctx, alphaSys, conversation.ReceiveParams{ChannelID: chAlpha.ID, From: "u3", FromName: "Prev", Text: "old"})
	if err != nil {
		t.Fatal(err)
	}
	// Beta 工作区数据，用于隔离检查
	cBeta, err := recv.Handle(ctx, betaSys, conversation.ReceiveParams{ChannelID: chBeta.ID, From: "b1", FromName: "BetaUser", Text: "secret"})
	if err != nil {
		t.Fatal(err)
	}

	// 调整会话时间、语言、团队归属与标签
	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, language = 'en', team_id = ?, status = 'closed', waiting_since = NULL WHERE id = ?", day1, team1.ID, c1.ConversationID)
	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, language = 'hi-Latn', team_id = ?, status = 'open', assignee_type = 'member', assignee_id = ? WHERE id = ?", day2, team2.ID, agentB.ID, c2.ConversationID)
	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, language = 'en', team_id = ?, status = 'closed', waiting_since = NULL WHERE id = ?", prevDay, team1.ID, cPrev.ConversationID)
	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, language = 'en' WHERE id = ?", day1, cBeta.ConversationID)
	if _, err := conversation.NewAddTag(env.DB, conversation.Downstream{}).Handle(ctx, alphaSys, c1.ConversationID, vipTag.ID); err != nil {
		t.Fatal(err)
	}

	// 清空默认自动生成的轮次，精确写入测试轮次：
	// - c1 Round 1 (day1): agentA 首响 120s，转给 agentB 在 600s 关闭，评 5 星
	// - c1 Round 2 (day2, 重开): agentB 首响 600s，在 1200s 关闭，评 2 星
	// - c2 Round 1 (day2, 进行中): agentB 已首响 300s，未关闭
	// - cPrev Round 1 (prevDay): 上一对比期，首响 200s，关闭 800s，评 4 星
	// - cBeta Round 1 (day1): 另一工作区，评 1 星
	mustExec(t, env.DB, "DELETE FROM conversation_rounds WHERE conversation_id IN (?)", bun.List([]string{c1.ConversationID, c2.ConversationID, cPrev.ConversationID, cBeta.ConversationID}))

	insertRound := func(tenantID, convID string, roundNo int, teamID *string, firstBy, assignee string, opened time.Time, frtSec, resSec int, score int, comment string) {
		r := &conversation.Round{
			ID:             uuid.Must(uuid.NewV7()).String(),
			TenantID:       tenantID,
			ConversationID: convID,
			Round:          roundNo,
			TeamID:         teamID,
			OpenedAt:       opened,
		}
		if frtSec >= 0 {
			frt := opened.Add(time.Duration(frtSec) * time.Second)
			r.FirstReplyAt = &frt
			r.FirstReplyByType = conversation.MemberActor
			r.FirstReplyByID = firstBy
		}
		if resSec >= 0 {
			cl := opened.Add(time.Duration(resSec) * time.Second)
			r.ClosedAt = &cl
			r.ClosedByType = conversation.MemberActor
			r.ClosedByID = assignee
			r.AssigneeType = conversation.MemberActor
			r.AssigneeID = assignee
		}
		if score > 0 {
			sc := score
			rt := opened.Add(time.Duration(resSec+60) * time.Second)
			r.Rating = &sc
			r.RatingComment = comment
			r.RatedAt = &rt
		}
		if _, err := env.DB.NewInsert().Model(r).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	insertRound(alpha.ID, c1.ConversationID, 1, &team1.ID, agentA.ID, agentB.ID, day1, 120, 600, 5, "very fast")
	insertRound(alpha.ID, c1.ConversationID, 2, &team1.ID, agentB.ID, agentB.ID, day2, 600, 1200, 2, "took longer")
	insertRound(alpha.ID, c2.ConversationID, 1, &team2.ID, agentB.ID, "", day2, 300, -1, 0, "")
	insertRound(alpha.ID, cPrev.ConversationID, 1, &team1.ID, agentA.ID, agentA.ID, prevDay, 200, 800, 4, "ok")
	insertRound(beta.ID, cBeta.ConversationID, 1, nil, beta.OwnerID, beta.OwnerID, day1, 50, 100, 1, "beta")

	// 插入成员回复消息与客户语言识别结果
	mustExec(t, env.DB, "UPDATE timeline_items SET created_at = ?, derived = '{\"language\":\"en\"}'::jsonb WHERE conversation_id = ?", day1, c1.ConversationID)
	mustExec(t, env.DB, "UPDATE timeline_items SET created_at = ?, derived = '{\"language\":\"hi-Latn\"}'::jsonb WHERE conversation_id = ?", day2, c2.ConversationID)
	mustExec(t, env.DB, "UPDATE timeline_items SET created_at = ?, derived = '{\"language\":\"ja\"}'::jsonb WHERE conversation_id = ?", day1, cBeta.ConversationID)
	// 插入一条成员带译文回复
	replyItem := &conversation.Item{
		ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day2.Add(5 * time.Minute), UpdatedAt: day2.Add(5 * time.Minute), TenantID: alpha.ID, ConversationID: c2.ConversationID, Seq: 99,
		Kind: conversation.Message, ActorType: conversation.MemberActor, ActorID: agentB.ID, Visibility: conversation.Everyone,
		Content: conversation.Content{Text: "namaste ji", Original: "您好", Language: "hi-Latn"}, Derived: map[string]any{},
	}
	if _, err := env.DB.NewInsert().Model(replyItem).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	// 插入翻译 llm_calls（alpha 2 成功 + 1 失败，beta 1 成功）
	cost1, cost2 := 0.001, 0.002
	for _, rec := range []*llm.Call{
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day1, UpdatedAt: day1, TenantID: &alpha.ID, CallType: llm.Translation, Operation: "incoming", Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 100, CompletionTokens: 50, ReasoningTokens: 10, Cost: &cost1, DurationMS: 400},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day2, UpdatedAt: day2, TenantID: &alpha.ID, CallType: llm.Translation, Operation: "reply", Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 200, CompletionTokens: 100, Cost: &cost2, DurationMS: 600},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day2, UpdatedAt: day2, TenantID: &alpha.ID, CallType: llm.Translation, Operation: "reply", Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "", PromptTokens: 0, CompletionTokens: 0, DurationMS: 800, Error: "timeout"},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: prevDay, UpdatedAt: prevDay, TenantID: &alpha.ID, CallType: llm.Translation, Operation: "incoming", Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m1", Request: map[string]any{}, Response: "{}", PromptTokens: 80, CompletionTokens: 20, Cost: &cost1, DurationMS: 300},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: day1, UpdatedAt: day1, TenantID: &beta.ID, CallType: llm.Translation, Operation: "incoming", Meta: map[string]any{}, Endpoint: "https://openrouter.ai/api/v1", Model: "m-beta", Request: map[string]any{}, Response: "{}", PromptTokens: 999, CompletionTokens: 999, Cost: &cost2, DurationMS: 100},
	} {
		if _, err := env.DB.NewInsert().Model(rec).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 停用 team2 并移出 agentA，验证历史报表仍保留两者
	if _, err := team.NewDisable(env.DB).Handle(ctx, alphaSys, team2.ID); err != nil {
		t.Fatal(err)
	}
	if err := env.Roles.Unassign(ctx, identity.Ref{Type: member.Type, ID: agentA.ID}, alpha.ID, agentRole.ID); err != nil {
		t.Fatal(err)
	}

	p := period.Params{From: "2026-11-10", To: "2026-11-12", TimeZone: "UTC"}
	f := report.FilterParams{Params: p}
	alphaOwner := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values()))}

	// 1. 会话报表
	convRep, err := report.NewConversations(env.DB).Handle(ctx, alphaOwner, f)
	if err != nil {
		t.Fatal(err)
	}
	if convRep.Summary.Created != 2 || convRep.Summary.PrevCreated != 1 || convRep.Summary.Reopened != 1 || convRep.Summary.Closed != 2 || convRep.Summary.PrevClosed != 1 {
		t.Fatalf("conversations summary=%+v", convRep.Summary)
	}
	if len(convRep.Series) != 3 || convRep.Series[0].Date != "2026-11-10" || convRep.Series[2].Created != 0 {
		t.Fatalf("conversations series=%+v", convRep.Series)
	}
	if len(convRep.ByTag) != 1 || convRep.ByTag[0].Name != "VIP" {
		t.Fatalf("conversations by_tag=%+v", convRep.ByTag)
	}
	// 停用的 team2 仍保留在团队分布中
	foundDisabled := false
	for _, tm := range convRep.ByTeam {
		if tm.ID != nil && *tm.ID == team2.ID && tm.Disabled && tm.Created == 1 {
			foundDisabled = true
		}
	}
	if !foundDisabled {
		t.Fatalf("disabled team2 missing from by_team: %+v", convRep.ByTeam)
	}

	// 2. 响应时间报表：FRT 样本 [120, 300, 600] → median=300, avg=340；解决时长 [600, 1200] → median=900, avg=900
	rtRep, err := report.NewResponseTime(env.DB).Handle(ctx, alphaOwner, f)
	if err != nil {
		t.Fatal(err)
	}
	if rtRep.FirstReply.Samples != 3 || rtRep.FirstReply.MedianSeconds == nil || *rtRep.FirstReply.MedianSeconds != 300 || *rtRep.FirstReply.AvgSeconds != 340 {
		t.Fatalf("frt stats=%+v", rtRep.FirstReply)
	}
	if rtRep.FirstReply.PrevSamples != 1 || rtRep.FirstReply.PrevMedianSeconds == nil || *rtRep.FirstReply.PrevMedianSeconds != 200 {
		t.Fatalf("prev frt stats=%+v", rtRep.FirstReply)
	}
	if rtRep.Resolution.Samples != 2 || rtRep.Resolution.MedianSeconds == nil || *rtRep.Resolution.MedianSeconds != 900 {
		t.Fatalf("resolution stats=%+v", rtRep.Resolution)
	}

	// 3. 满意度报表与分页明细：评价 [5, 2] → CSAT=50%, Avg=3.5, ResponseRate=100%
	satRep, err := report.NewSatisfaction(env.DB).Handle(ctx, alphaOwner, f)
	if err != nil {
		t.Fatal(err)
	}
	if satRep.Summary.RatedCount != 2 || satRep.Summary.PositiveCount != 1 || *satRep.Summary.CSAT != 50 || *satRep.Summary.AvgScore != 3.5 || *satRep.Summary.PrevCSAT != 100 {
		t.Fatalf("satisfaction summary=%+v", satRep.Summary)
	}
	ratings, err := report.NewListRatings(env.DB).Handle(ctx, alphaOwner, report.RatingsInput{
		FilterParams: f,
		OffsetParams: page.OffsetParams{Page: 1, PerPage: 10},
		MaxScore:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ratings.Total != 1 || len(ratings.Items) != 1 || ratings.Items[0].Score != 2 || ratings.Items[0].Comment != "took longer" {
		t.Fatalf("low score ratings=%+v", ratings)
	}

	// 4. 成员报表：agentA 虽然已移出，但 FRT(120s) 准确归因到他且 Removed=true；agentB 关闭 2 轮、回复 1 条
	memRep, err := report.NewMembers(env.DB, env.Roles, env.Files).Handle(ctx, alphaOwner, f)
	if err != nil {
		t.Fatal(err)
	}
	byMember := map[string]report.MemberMetrics{}
	for _, m := range memRep.Items {
		byMember[m.MemberID] = m
	}
	mA, okA := byMember[agentA.ID]
	mB, okB := byMember[agentB.ID]
	if !okA || !mA.Removed || mA.FirstReplySamples != 1 || *mA.FirstReplyMedian != 120 || mA.ClosedCount != 0 {
		t.Fatalf("agentA metrics=%+v (ok=%v)", mA, okA)
	}
	if !okB || mB.Removed || mB.ClosedCount != 2 || mB.RepliesSent != 1 || mB.FirstReplySamples != 2 || *mB.FirstReplyMedian != 450 || mB.OpenAssigned != 1 {
		t.Fatalf("agentB metrics=%+v (ok=%v)", mB, okB)
	}
	// 按 team_id 筛选成员报表不应报缺列错误
	mustExec(t, env.DB, "INSERT INTO team_members (team_id, member_id, created_at, updated_at) VALUES (?, ?, ?, ?)", team1.ID, agentB.ID, day1, day1)
	teamMemRep, err := report.NewMembers(env.DB, env.Roles, env.Files).Handle(ctx, alphaOwner, report.FilterParams{Params: p, TeamID: team1.ID})
	if err != nil {
		t.Fatalf("members filter by team failed: %v", err)
	}
	if len(teamMemRep.Items) == 0 {
		t.Fatalf("expected members for team1")
	}

	// 上一对比期关闭的轮次在本期被评价时，参评率仍应按本期关闭轮次计算且不超过 100%
	prevConv, err := recv.Handle(ctx, alphaSys, conversation.ReceiveParams{ChannelID: chAlpha.ID, From: "u-cross", FromName: "Cross", Text: "old"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, env.DB, "UPDATE conversations SET created_at = ?, team_id = ?, status = 'closed', waiting_since = NULL WHERE id = ?", prevDay, team1.ID, prevConv.ConversationID)
	mustExec(t, env.DB, "DELETE FROM conversation_rounds WHERE conversation_id = ?", prevConv.ConversationID)
	score5 := 5
	crossRound := &conversation.Round{
		ID: uuid.Must(uuid.NewV7()).String(), TenantID: alpha.ID, ConversationID: prevConv.ConversationID, Round: 1,
		TeamID: &team1.ID, OpenedAt: prevDay, ClosedAt: &prevDay, ClosedByType: conversation.MemberActor, ClosedByID: agentB.ID,
		AssigneeType: conversation.MemberActor, AssigneeID: agentB.ID, Rating: &score5, RatedAt: &day2,
	}
	if _, err := env.DB.NewInsert().Model(crossRound).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	satCross, err := report.NewSatisfaction(env.DB).Handle(ctx, alphaOwner, f)
	if err != nil {
		t.Fatal(err)
	}
	if satCross.Summary.RatedCount != 3 || satCross.Summary.ClosedCount != 2 || satCross.Summary.ResponseRate == nil || *satCross.Summary.ResponseRate != 100 {
		t.Fatalf("cross-period rating response rate should stay <= 100%%, got %+v", satCross.Summary)
	}

	// 5. 翻译报表：alpha 3 次调用（2 成功 1 失败），450 token，0.003 USD，不混入 beta 的 m-beta
	trRep, err := report.NewTranslation(env.DB).Handle(ctx, alphaOwner, p)
	if err != nil {
		t.Fatal(err)
	}
	if trRep.Summary.Calls != 3 || trRep.Summary.FailedCalls != 1 || trRep.Summary.TotalTokens != 450 || trRep.Summary.ReasoningTokens != 10 || trRep.Summary.TotalCostUSD != 0.003 || trRep.Summary.PrevCalls != 1 {
		t.Fatalf("translation summary=%+v", trRep.Summary)
	}
	if len(trRep.ByModel) != 1 || trRep.ByModel[0].Model != "m1" {
		t.Fatalf("translation by_model=%+v", trRep.ByModel)
	}
	if len(trRep.ByOperation) != 2 {
		t.Fatalf("translation by_operation=%+v", trRep.ByOperation)
	}
	if len(trRep.ReplyLanguages) != 1 || trRep.ReplyLanguages[0].Language != "hi-Latn" {
		t.Fatalf("translation reply_languages=%+v", trRep.ReplyLanguages)
	}

	// 6. 跨工作区筛选校验：在 alpha 用 beta 的渠道 ID 筛选返回 channel.ErrNotFound
	if _, err := report.NewConversations(env.DB).Handle(ctx, alphaOwner, report.FilterParams{Params: p, ChannelID: chBeta.ID}); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("cross-tenant channel filter err=%v, want channel.ErrNotFound", err)
	}
}

func mustExec(t *testing.T, db *bun.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// TestEmptyWorkspaceReportsReturnEmptyArrays 验证空工作区的全部报表在 JSON 序列化时均输出空数组 []，不返回 null。
func TestEmptyWorkspaceReportsReturnEmptyArrays(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "empty", Slug: "empty", OwnerEmail: "owner@empty.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{
		Type:        member.Type,
		ID:          tn.OwnerID,
		Tenant:      tn.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	p := period.Params{From: "2026-11-19", To: "2026-11-25", TimeZone: "UTC"}
	f := report.FilterParams{Params: p}

	assertNonNullArrays := func(name string, v any, fields ...string) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("%s unmarshal: %v", name, err)
		}
		for _, field := range fields {
			if _, ok := obj[field].([]any); !ok {
				t.Fatalf("%s.%s should be JSON array, got %#v (raw=%s)", name, field, obj[field], string(raw))
			}
		}
	}

	conv, err := report.NewConversations(env.DB).Handle(ctx, sub, f)
	if err != nil {
		t.Fatal(err)
	}
	assertNonNullArrays("conversations", conv, "series", "by_channel", "by_team", "by_tag", "by_language")

	rt, err := report.NewResponseTime(env.DB).Handle(ctx, sub, f)
	if err != nil {
		t.Fatal(err)
	}
	assertNonNullArrays("response_time", rt, "series", "first_reply_buckets", "resolution_buckets", "by_team", "by_channel")

	sat, err := report.NewSatisfaction(env.DB).Handle(ctx, sub, f)
	if err != nil {
		t.Fatal(err)
	}
	assertNonNullArrays("satisfaction", sat, "distribution", "series", "by_team", "by_channel")

	mem, err := report.NewMembers(env.DB, env.Roles, env.Files).Handle(ctx, sub, f)
	if err != nil {
		t.Fatal(err)
	}
	assertNonNullArrays("members", mem, "items")

	tr, err := report.NewTranslation(env.DB).Handle(ctx, sub, p)
	if err != nil {
		t.Fatal(err)
	}
	assertNonNullArrays("translation", tr, "series", "by_operation", "by_model", "detected_languages", "reply_languages")
}
