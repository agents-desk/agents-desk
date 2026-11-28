package agent_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue/queuetest"

	"agents-desk/internal/agent"
	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
	"agents-desk/internal/translation"
)

// scriptedModel 支持多轮响应编排并捕获每次发送给模型的请求，用于全链路运行时测试。
type scriptedModel struct {
	mu       sync.Mutex
	requests []*model.Request
	handler  func(ctx context.Context, req *model.Request, call int) ([]*model.Response, error)
}

func (m *scriptedModel) Info() model.Info { return model.Info{Name: "scripted-chat"} }

func (m *scriptedModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	callIdx := len(m.requests)
	m.requests = append(m.requests, req)
	m.mu.Unlock()

	rsps, err := m.handler(ctx, req, callIdx)
	if err != nil {
		return nil, err
	}
	ch := make(chan *model.Response, len(rsps))
	for _, r := range rsps {
		ch <- r
	}
	close(ch)
	return ch, nil
}

func (m *scriptedModel) recorded() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*model.Request, len(m.requests))
	copy(out, m.requests)
	return out
}

// fakeRetriever 记录检索入参并返回预置引用切片。
type fakeRetriever struct {
	mu       sync.Mutex
	requests []agent.RetrieveRequest
	cites    []agent.Citation
}

func (r *fakeRetriever) Retrieve(_ context.Context, req agent.RetrieveRequest) ([]agent.Citation, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return r.cites, nil
}

// TestChatRuntime_AutoCopilotIsolationAndLanguage 验证：
// 1. Bot CRUD 与占用删除拦截（ErrBotInUse）；
// 2. ai_auto 外部接待严格隔离内部备注与团队可见内容，检索仅传入 KnowledgeExternal，写入 after_seq/generation_id 与 ai_generation_count；
// 3. 多语言提示词锁与术语表生效；
// 4. ai_copilot 生成 ModeDraft 草稿、List 返回未采纳草稿、Reply 带 source_generation_id 采纳并标记 ai_assisted；
// 5. handover_to_human 工具触发后自动暂停 AI 并释放 Bot 指派；
// 6. 预算熔断错误自动转 ai_paused。
func TestChatRuntime_AutoCopilotIsolationAndLanguage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "BetaOwner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	owner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	otherOwner := identity.Subject{Type: member.Type, ID: other.OwnerID, Tenant: other.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}

	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{
		Sinks: []conversation.Sink{{Topic: conversation.DeliverTopic, Accept: conversation.Delivers}},
		Hub:   hub, Roles: env.Roles,
	}

	// 新建并更新机器人
	bot, err := agent.NewCreateBot(env.DB).Handle(ctx, owner, agent.BotInput{
		Name: "售前AI客服",
		Settings: agent.BotSettings{
			SystemPrompt:  "Be concise.",
			PromptVersion: "v2.1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{
		Kind: channel.Test,
		Name: "官网渠道",
		Settings: map[string]any{
			"ai_reception": map[string]any{
				"enabled": true,
				"bot_id":  bot.ID,
				"mode":    "ai_auto",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 正在被渠道 AI 自动接待占用的机器人不允许删除
	if err := agent.NewDeleteBot(env.DB).Handle(ctx, owner, bot.ID); !errors.Is(err, agent.ErrBotInUse) {
		t.Fatalf("被渠道接待占用的机器人应禁止删除: %v", err)
	}

	// 配置工作区翻译术语表与语气
	trGroup := translation.NewGroup(env.DB)
	trActions := translation.NewSettingsActions(env.DB, trGroup, translation.NewTranslator(env.DB, llm.NewClient(env.DB, llm.NewGroup(env.DB, env.Crypt), nil), trGroup))
	if _, err := trActions.SaveTone(ctx, owner, translation.Friendly); err != nil {
		t.Fatal(err)
	}
	if _, err := trActions.SaveGlossary(ctx, owner, []translation.Term{{Term: "TenonDesk", Keep: true}}); err != nil {
		t.Fatal(err)
	}

	// 客户发消息，并先插入一条内部备注验证对外部 AI 严格隔离
	rec, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c-hi", FromName: "Rahul", Text: "mera order status?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`UPDATE conversations SET language = 'hi-Latn' WHERE id = ?`, rec.ConversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewAddNote(env.DB, down).Handle(ctx, owner, rec.ConversationID, conversation.NoteBody{Text: "SECRET_INTERNAL_VIP_FLAG"}); err != nil {
		t.Fatal(err)
	}

	retriever := &fakeRetriever{
		cites: []agent.Citation{
			{DocumentID: "doc-1", ChunkID: "chk-1", Title: "物流政策", Snippet: "48小时内发货", Score: 0.92, Visibility: agent.KnowledgeExternal},
		},
	}

	var modePhase atomic.Int32
	modelMock := &scriptedModel{
		handler: func(_ context.Context, req *model.Request, _ int) ([]*model.Response, error) {
			// 第0轮调用 search_knowledge，随后返回回答；或按 modePhase 控制不同场景
			switch modePhase.Load() {
			case 0:
				// 检查未在任何消息中出现工具结果之前先调用 search_knowledge
				hasToolResult := false
				for _, msg := range req.Messages {
					if msg.Role == model.RoleTool {
						hasToolResult = true
					}
					if strings.Contains(msg.Content, "SECRET_INTERNAL_VIP_FLAG") {
						return nil, errors.New("internal note leaked to external AI prompt")
					}
				}
				if !hasToolResult {
					return []*model.Response{{
						Done: true,
						Choices: []model.Choice{{
							Message: model.Message{
								Role: model.RoleAssistant,
								ToolCalls: []model.ToolCall{{
									ID:   "call-rag-1",
									Type: "function",
									Function: model.FunctionDefinitionParam{
										Name:      "search_knowledge",
										Arguments: []byte(`{"query":"order shipping"}`),
									},
								}},
							},
						}},
					}}, nil
				}
				return []*model.Response{
					{IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Role: model.RoleAssistant, Content: "Aapka order TenonDesk "}}}},
					{Done: true, Usage: &model.Usage{PromptTokens: 20, CompletionTokens: 12}, Choices: []model.Choice{{Message: model.NewAssistantMessage("Aapka order TenonDesk 48h mein ship hoga.")}}},
				}, nil
			case 1:
				// Copilot 模式：允许读取内部备注辅助坐席起草
				return []*model.Response{
					{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("建议回复：您好，已为您加急处理。")}}},
				}, nil
			case 2:
				// handover_to_human 工具测试
				hasToolResult := false
				for _, msg := range req.Messages {
					if msg.Role == model.RoleTool {
						hasToolResult = true
					}
				}
				if !hasToolResult {
					return []*model.Response{{
						Done: true,
						Choices: []model.Choice{{
							Message: model.Message{
								Role: model.RoleAssistant,
								ToolCalls: []model.ToolCall{{
									ID:       "call-handover",
									Type:     "function",
									Function: model.FunctionDefinitionParam{Name: "handover_to_human", Arguments: []byte(`{"reason":"complex_refund"}`)},
								}},
							},
						}},
					}}, nil
				}
				return []*model.Response{
					{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("正在为您转接人工客服处理。")}}},
				}, nil
			default:
				return nil, llm.ErrBudgetExceeded.New()
			}
		},
	}

	svc := agent.NewService(env.DB, down, hub, nil, nil, trGroup,
		agent.WithRetriever(retriever),
		agent.WithModelOverride(modelMock),
	)
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })
	down.OnGenerationCanceled = svc.CancelRun
	svc.SetDownstream(down)

	// 执行 ai_auto 队列任务
	if err := svc.Runner().Job(ctx, conversation.Appended{
		TenantID: tn.ID, ConversationID: rec.ConversationID, ItemID: rec.Item.ID, Seq: rec.Item.Seq,
	}); err != nil {
		t.Fatal(err)
	}

	// 检查语言锁、术语表与 RAG 外部可见性隔离
	reqs := modelMock.recorded()
	if len(reqs) < 2 {
		t.Fatalf("应至少有工具调用和最终回复两轮模型请求: %d", len(reqs))
	}
	sysMsg := reqs[0].Messages[0].Content
	if !strings.Contains(sysMsg, `"hi-Latn"`) || !strings.Contains(sysMsg, `"TenonDesk"`) {
		t.Fatalf("系统提示词应包含语言锁与术语表约束: %s", sysMsg)
	}
	if len(retriever.requests) != 1 || retriever.requests[0].Visibility != agent.KnowledgeExternal || retriever.requests[0].TenantID != tn.ID {
		t.Fatalf("外部接待知识检索必须锁定 TenantID 与 KnowledgeExternal: %+v", retriever.requests)
	}

	// 检查生成归因字段与时间线消息
	var genRow struct {
		ID            string           `bun:"id"`
		Mode          agent.Mode       `bun:"mode"`
		TriggerSeq    int64            `bun:"trigger_seq"`
		PromptVersion string           `bun:"prompt_version"`
		Citations     []agent.Citation `bun:"citations,type:jsonb"`
	}
	if err := env.DB.NewRaw(`SELECT id, mode, trigger_seq, prompt_version, citations FROM generations WHERE trigger_item_id = ?`, rec.Item.ID).Scan(ctx, &genRow); err != nil {
		t.Fatal(err)
	}
	if genRow.Mode != agent.ModeReply || genRow.TriggerSeq != rec.Item.Seq || genRow.PromptVersion != "v2.1" || len(genRow.Citations) != 1 {
		t.Fatalf("生成元数据异常: %+v", genRow)
	}
	var roundGenCount int
	if err := env.DB.NewRaw(`SELECT ai_generation_count FROM conversation_rounds WHERE conversation_id = ? AND closed_at IS NULL`, rec.ConversationID).Scan(ctx, &roundGenCount); err != nil {
		t.Fatal(err)
	}
	if roundGenCount != 1 {
		t.Fatalf("当前服务轮次 ai_generation_count 应为 1: got=%d", roundGenCount)
	}

	// 切换到 ai_copilot 模式，验证生成草稿且不直接写入时间线，人工回复采纳后从活跃草稿隐藏
	modePhase.Store(1)
	if _, err := conversation.NewSetHandlingMode(env.DB, down).Handle(ctx, owner, rec.ConversationID, conversation.HandlingModeBody{
		Mode: conversation.HandlingAICopilot, BotID: bot.ID,
	}); err != nil {
		t.Fatal(err)
	}
	rec2, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c-hi", FromName: "Rahul", Text: "can you expedite?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Runner().Job(ctx, conversation.Appended{
		TenantID: tn.ID, ConversationID: rec.ConversationID, ItemID: rec2.Item.ID, Seq: rec2.Item.Seq,
	}); err != nil {
		t.Fatal(err)
	}
	drafts, err := agent.NewList(env.DB).Handle(ctx, owner, rec.ConversationID)
	if err != nil || len(drafts) != 1 || drafts[0].Mode != agent.ModeDraft || drafts[0].State != agent.Completed {
		t.Fatalf("Copilot 模式应返回一条已完成草稿: %+v err=%v", drafts, err)
	}
	// 跨工作区成员无法查看草稿或生成列表
	if _, err := agent.NewList(env.DB).Handle(ctx, otherOwner, rec.ConversationID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("跨工作区成员不应看到生成列表: %v", err)
	}
	// 成员采纳草稿发送回复后，草稿已关联 generation_id 并从待采纳列表消失
	adopted, err := conversation.NewReply(env.DB, down).Handle(ctx, owner, rec.ConversationID, conversation.ReplyBody{
		Text: "您好，已为您加急处理。", SourceGenerationID: drafts[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Actor.Type != conversation.MemberActor {
		t.Fatalf("采纳回复作者应为成员: %+v", adopted.Actor)
	}
	draftsAfter, err := agent.NewList(env.DB).Handle(ctx, owner, rec.ConversationID)
	if err != nil || len(draftsAfter) != 0 {
		t.Fatalf("采纳后的草稿不应再列为待采纳: %+v %v", draftsAfter, err)
	}

	// 验证 handover_to_human 工具将 ai_auto 会话自动切换回 ai_paused 并释放 Bot
	modePhase.Store(2)
	if _, err := conversation.NewSetHandlingMode(env.DB, down).Handle(ctx, owner, rec.ConversationID, conversation.HandlingModeBody{
		Mode: conversation.HandlingAIAuto, BotID: bot.ID,
	}); err != nil {
		t.Fatal(err)
	}
	rec3, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c-hi", FromName: "Rahul", Text: "I want full refund!",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Runner().Job(ctx, conversation.Appended{
		TenantID: tn.ID, ConversationID: rec.ConversationID, ItemID: rec3.Item.ID, Seq: rec3.Item.Seq,
	}); err != nil {
		t.Fatal(err)
	}
	afterHandover, err := conversation.NewGet(env.DB).Handle(ctx, owner, rec.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if afterHandover.HandlingMode != conversation.HandlingAIPaused || afterHandover.Assignee != nil {
		t.Fatalf("handover_to_human 应暂停 AI 并释放 Bot 指派: mode=%s assignee=%+v", afterHandover.HandlingMode, afterHandover.Assignee)
	}

	// 验证预算熔断时自动切为 ai_paused 并记录 handover_reason = budget_exceeded
	modePhase.Store(3)
	if _, err := conversation.NewSetHandlingMode(env.DB, down).Handle(ctx, owner, rec.ConversationID, conversation.HandlingModeBody{
		Mode: conversation.HandlingAIAuto, BotID: bot.ID,
	}); err != nil {
		t.Fatal(err)
	}
	rec4, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c-hi", FromName: "Rahul", Text: "still there?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Runner().Job(ctx, conversation.Appended{
		TenantID: tn.ID, ConversationID: rec.ConversationID, ItemID: rec4.Item.ID, Seq: rec4.Item.Seq,
	}); err != nil {
		t.Fatal(err)
	}
	afterBudget, err := conversation.NewGet(env.DB).Handle(ctx, owner, rec.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if afterBudget.HandlingMode != conversation.HandlingAIPaused || afterBudget.Assignee != nil {
		t.Fatalf("预算熔断后应自动切换 ai_paused 并释放 Bot: mode=%s assignee=%+v", afterBudget.HandlingMode, afterBudget.Assignee)
	}
	var handoverReason string
	if err := env.DB.NewRaw(`SELECT handover_reason FROM conversation_rounds WHERE conversation_id = ? AND closed_at IS NULL`, rec.ConversationID).Scan(ctx, &handoverReason); err != nil {
		t.Fatal(err)
	}
	if handoverReason != "budget_exceeded" {
		t.Fatalf("轮次 handover_reason 应记录 budget_exceeded: got=%s", handoverReason)
	}
}

// TestChatRuntime_HighRiskToolApprovalAndBargeIn 验证高风险工具的人工审批（批准续跑与拒绝理由续跑）及客户插话策略。
func TestChatRuntime_HighRiskToolApprovalAndBargeIn(t *testing.T) {
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
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{
		Sinks: []conversation.Sink{{Topic: conversation.DeliverTopic, Accept: conversation.Delivers}},
		Hub:   hub, Roles: env.Roles,
	}

	var refundExecuted atomic.Int32
	var seenTenantInTool string
	reg := agent.NewRegistry()
	reg.Register(agent.ToolSpec{
		Name:            "refund_order",
		Description:     "Issue customer order refund",
		Risk:            agent.RiskHighImpact,
		ExternalAllowed: true,
		Build: func(_ *agent.Service) tool.CallableTool {
			return function.NewFunctionTool(
				func(ctx context.Context, in struct {
					OrderID string `json:"order_id"`
				}) (map[string]any, error) {
					exec, ok := agent.ExecutionContextFrom(ctx)
					if !ok {
						return nil, errors.New("missing execution context")
					}
					seenTenantInTool = exec.TenantID
					refundExecuted.Add(1)
					return map[string]any{"refunded": true, "order_id": in.OrderID}, nil
				},
				function.WithName("refund_order"),
				function.WithDescription("Refund customer order"),
			)
		},
	})

	bot, err := agent.NewCreateBot(env.DB).Handle(ctx, owner, agent.BotInput{
		Name: "退款专员",
		Settings: agent.BotSettings{
			AllowedTools:  []string{"refund_order"},
			BargeInPolicy: agent.BargeInCancelRestart,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{
		Kind: channel.Test, Name: "售后渠道",
		Settings: map[string]any{"ai_reception": map[string]any{"enabled": true, "bot_id": bot.ID, "mode": "ai_auto"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var rejectPhase atomic.Bool
	modelMock := &scriptedModel{
		handler: func(_ context.Context, req *model.Request, _ int) ([]*model.Response, error) {
			for _, msg := range req.Messages {
				if msg.Role == model.RoleTool {
					if strings.Contains(msg.Content, "超出退货期") {
						return []*model.Response{
							{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("抱歉，因超出退货期无法退款。")}}},
						}, nil
					}
					return []*model.Response{
						{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("您的退款已批准处理完成。")}}},
					}, nil
				}
			}
			callID := "call-refund-approve"
			if rejectPhase.Load() {
				callID = "call-refund-reject"
			}
			return []*model.Response{{
				Done: true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   callID,
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "refund_order",
								Arguments: []byte(`{"order_id":"ORD-100"}`),
							},
						}},
					},
				}},
			}}, nil
		},
	}

	svc := agent.NewService(env.DB, down, hub, nil, nil, nil,
		agent.WithRegistry(reg),
		agent.WithModelOverride(modelMock),
	)
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })

	// 场景 A：高风险工具触发 waiting_approval -> 批准执行并自动完成恢复
	rec1, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c-1", FromName: "张三", Text: "请帮我退单 ORD-100",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Runner().Job(ctx, conversation.Appended{
		TenantID: tn.ID, ConversationID: rec1.ConversationID, ItemID: rec1.Item.ID, Seq: rec1.Item.Seq,
	}); err != nil {
		t.Fatal(err)
	}
	if refundExecuted.Load() != 0 {
		t.Fatalf("审批前不应执行高风险工具: count=%d", refundExecuted.Load())
	}
	conv1, err := conversation.NewGet(env.DB).Handle(ctx, owner, rec1.ConversationID)
	if err != nil || conv1.HandlingMode != conversation.HandlingWaitingApproval {
		t.Fatalf("会话应进入 waiting_approval: mode=%s err=%v", conv1.HandlingMode, err)
	}
	activeGens, err := agent.NewList(env.DB).Handle(ctx, owner, rec1.ConversationID)
	if err != nil || len(activeGens) != 1 || activeGens[0].State != agent.WaitingApproval || len(activeGens[0].PendingApprovals) != 1 {
		t.Fatalf("待审批生成异常: %+v err=%v", activeGens, err)
	}

	approver := agent.NewApproveToolCall(env.DB, svc)
	if _, err := approver.Approve(ctx, owner, activeGens[0].ID, "call-refund-approve"); err != nil {
		t.Fatal(err)
	}

	// 等待异步消费携程定稿完成
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var st agent.State
		if err := env.DB.NewRaw(`SELECT state FROM generations WHERE id = ?`, activeGens[0].ID).Scan(ctx, &st); err == nil && st == agent.Completed {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if refundExecuted.Load() != 1 || seenTenantInTool != tn.ID {
		t.Fatalf("批准后应在受信任租户上下文中执行一次高风险工具: exec=%d tenant=%s", refundExecuted.Load(), seenTenantInTool)
	}
	// 重复审批必须报 ErrApprovalNotPending
	if _, err := approver.Approve(ctx, owner, activeGens[0].ID, "call-refund-approve"); !errors.Is(err, agent.ErrApprovalNotPending) {
		t.Fatalf("已完成的生成再审批应报 ErrApprovalNotPending: %v", err)
	}
	afterApproveConv, err := conversation.NewGet(env.DB).Handle(ctx, owner, rec1.ConversationID)
	if err != nil || afterApproveConv.HandlingMode != conversation.HandlingAIAuto {
		t.Fatalf("审批完成后会话应恢复 ai_auto: mode=%s err=%v", afterApproveConv.HandlingMode, err)
	}

	// 场景 B：拒绝工具调用并把理由送回模型生成礼貌拒信
	rejectPhase.Store(true)
	rec2, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "c-2", FromName: "李四", Text: "退掉 ORD-200",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Runner().Job(ctx, conversation.Appended{
		TenantID: tn.ID, ConversationID: rec2.ConversationID, ItemID: rec2.Item.ID, Seq: rec2.Item.Seq,
	}); err != nil {
		t.Fatal(err)
	}
	gens2, err := agent.NewList(env.DB).Handle(ctx, owner, rec2.ConversationID)
	if err != nil || len(gens2) != 1 {
		t.Fatalf("场景B待审批生成异常: %+v err=%v", gens2, err)
	}
	if _, err := approver.Reject(ctx, owner, gens2[0].ID, "call-refund-reject", "超出退货期"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var st agent.State
		if err := env.DB.NewRaw(`SELECT state FROM generations WHERE id = ?`, gens2[0].ID).Scan(ctx, &st); err == nil && st == agent.Completed {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if refundExecuted.Load() != 1 {
		t.Fatalf("拒绝分支不得执行工具: exec=%d", refundExecuted.Load())
	}
	tl2, err := conversation.NewTimeline(env.DB).Handle(ctx, owner, rec2.ConversationID, rec2.Item.Seq, 10)
	if err != nil || len(tl2) == 0 || !strings.Contains(tl2[len(tl2)-1].Content.Text, "超出退货期") {
		t.Fatalf("拒绝后模型应生成包含原因的安抚回复: %+v err=%v", tl2, err)
	}
	if jobs := queuetest.Pending(t, env.DB, conversation.DeliverTopic); len(jobs) < 2 {
		t.Fatalf("审批与拒绝生成的回复都应交给投递队列: %d", len(jobs))
	}

	// 场景 C：低风险工具仅配置在机器人 RequireApprovalTools 中时，也必须经动态审批列表触发 waiting_approval
	var dynExecuted atomic.Int32
	regDyn := agent.NewRegistry()
	regDyn.Register(agent.ToolSpec{
		Name:            "tag_vip",
		Description:     "Tag conversation",
		Risk:            agent.RiskLowImpact,
		ExternalAllowed: true,
		Build: func(_ *agent.Service) tool.CallableTool {
			return function.NewFunctionTool(
				func(_ context.Context, _ struct{}) (map[string]any, error) {
					dynExecuted.Add(1)
					return map[string]any{"ok": true}, nil
				},
				function.WithName("tag_vip"),
				function.WithDescription("Tag conversation"),
			)
		},
	})
	botDyn, err := agent.NewCreateBot(env.DB).Handle(ctx, owner, agent.BotInput{
		Name: "动态审批专员",
		Settings: agent.BotSettings{
			AllowedTools:         []string{"tag_vip"},
			RequireApprovalTools: []string{"tag_vip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chDyn, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{
		Kind: channel.Test, Name: "动态审批渠道",
		Settings: map[string]any{"ai_reception": map[string]any{"enabled": true, "bot_id": botDyn.ID, "mode": "ai_auto"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dynModel := &scriptedModel{
		handler: func(_ context.Context, req *model.Request, _ int) ([]*model.Response, error) {
			for _, msg := range req.Messages {
				if msg.Role == model.RoleTool {
					return []*model.Response{{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("已打标。")}}}}, nil
				}
			}
			return []*model.Response{{
				Done: true,
				Choices: []model.Choice{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:       "call-dyn-tag",
							Type:     "function",
							Function: model.FunctionDefinitionParam{Name: "tag_vip", Arguments: []byte(`{}`)},
						}},
					},
				}},
			}}, nil
		},
	}
	svcDyn := agent.NewService(env.DB, down, hub, nil, nil, nil,
		agent.WithRegistry(regDyn),
		agent.WithModelOverride(dynModel),
	)
	t.Cleanup(func() { _ = svcDyn.Shutdown(context.Background()) })
	rec3, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: chDyn.ID, From: "c-3", FromName: "王五", Text: "请帮我打标",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svcDyn.Runner().Job(ctx, conversation.Appended{
		TenantID: tn.ID, ConversationID: rec3.ConversationID, ItemID: rec3.Item.ID, Seq: rec3.Item.Seq,
	}); err != nil {
		t.Fatal(err)
	}
	if dynExecuted.Load() != 0 {
		t.Fatalf("仅 RequireApprovalTools 配置的低风险工具审批前不得执行: exec=%d", dynExecuted.Load())
	}
	gens3, err := agent.NewList(env.DB).Handle(ctx, owner, rec3.ConversationID)
	if err != nil || len(gens3) != 1 || gens3[0].State != agent.WaitingApproval {
		t.Fatalf("动态审批工具应触发 waiting_approval: %+v err=%v", gens3, err)
	}
}
