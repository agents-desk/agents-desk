package agent

import (
	"context"
	"database/sql"
	"strings"
	"sync"

	"github.com/cockroachdb/errors"
	openaiopt "github.com/openai/openai-go/option"
	"github.com/uptrace/bun"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"

	"github.com/go-tenon/tenon/ai/chat"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/translation"
)

// ServiceOption 配置 Service 运行时的可选组件。
type ServiceOption func(*Service)

// WithRetriever 为 Service 注入工作区知识库检索实现。
func WithRetriever(r Retriever) ServiceOption {
	return func(s *Service) {
		if r != nil {
			s.retriever = r
		}
	}
}

// WithRegistry 为 Service 注入扩展工具注册表。
func WithRegistry(reg *Registry) ServiceOption {
	return func(s *Service) {
		if reg != nil {
			s.registry = reg
		}
	}
}

// WithModelOverride 注入测试或自定义模型实现，替代平台模型网关配置。
func WithModelOverride(m model.Model) ServiceOption {
	return func(s *Service) {
		s.modelOverride = m
	}
}

// Service 是统一托管于 tenon/ai/chat.Runs 的 Agent 运行时服务，协调上下文加载、工具安全隔离、
// 人工审批续跑、流式落库、预算熔断降级与平滑停机排空。
type Service struct {
	db            *bun.DB
	down          conversation.Downstream
	hub           *broadcast.Hub
	locker        *lock.Locker
	llm           *llm.Client
	trSettings    *settings.Group[translation.Settings]
	retriever     Retriever
	registry      *Registry
	modelOverride model.Model

	runs     *chat.Runs
	open     *Open
	append   *Append
	finalize *Finalize
	fail     *Fail
	cancel   *Cancel

	bgCtx    context.Context
	bgCancel context.CancelFunc
	wg       sync.WaitGroup

	collectorMu sync.Mutex
	collectors  map[string]*TraceCollector
}

// NewService 创建并装配 Agent 运行时服务。
func NewService(
	db *bun.DB,
	down conversation.Downstream,
	hub *broadcast.Hub,
	locker *lock.Locker,
	llmClient *llm.Client,
	trSettings *settings.Group[translation.Settings],
	opts ...ServiceOption,
) *Service {
	bgCtx, bgCancel := context.WithCancel(context.Background())
	s := &Service{
		db:         db,
		down:       down,
		hub:        hub,
		locker:     locker,
		llm:        llmClient,
		trSettings: trSettings,
		retriever:  EmptyRetriever{},
		registry:   NewRegistry(),
		open:       NewOpen(db),
		append:     NewAppend(db, hub),
		finalize:   NewFinalize(db, down, hub),
		fail:       NewFail(db, hub),
		bgCtx:      bgCtx,
		bgCancel:   bgCancel,
		collectors: map[string]*TraceCollector{},
	}
	s.cancel = NewCancel(db, hub, s.CancelRun)
	for _, opt := range opts {
		opt(s)
	}

	runOpts := []chat.Option{
		chat.UseHistory(newHistoryStore(db)),
		chat.AgentFor(s.agentFor),
		chat.RequireApprovalFunc(s.approvalsFor),
	}
	if hub != nil && locker != nil {
		runOpts = append(runOpts, chat.Cluster(hub, locker))
	}
	s.runs = chat.NewRuns("agents-desk", nil, nil, runOpts...)
	return s
}

// SetDownstream 更新下游配置并同步到底层 Finalize 动作，供启动装配后绑定循环回调使用。
func (s *Service) SetDownstream(down conversation.Downstream) {
	s.down = down
	s.finalize = NewFinalize(s.db, down, s.hub)
}

// Runner 返回绑定当前服务的队列执行器。
func (s *Service) Runner() *Runner {
	return NewRunner(s.db, s.hub, s)
}

// CancelRun 取消会话中正在运行的 ai/chat 任务。
func (s *Service) CancelRun(ctx context.Context, tenantID, convID string) {
	if s == nil || s.runs == nil {
		return
	}
	r, err := s.runs.Get(ctx, tenantID, convID)
	if err == nil && r != nil {
		r.Cancel()
	}
}

// Shutdown 平滑停止 Agent 服务，等待审批续跑等后台消费协程退出并排空 ai/chat.Runs。
func (s *Service) Shutdown(ctx context.Context) error {
	var runErr error
	if s.runs != nil {
		runErr = s.runs.Shutdown(ctx)
	}
	s.bgCancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return runErr
}

// Close 立即释放底层 ai/chat.Runs 资源。
func (s *Service) Close() error {
	s.bgCancel()
	if s.runs != nil {
		return s.runs.Close()
	}
	return nil
}

// collectorFor 返回或初始化生成 gID 对应的线程安全轨迹收集器。
func (s *Service) collectorFor(genID string) *TraceCollector {
	s.collectorMu.Lock()
	defer s.collectorMu.Unlock()
	c, ok := s.collectors[genID]
	if !ok {
		c = NewTraceCollector()
		s.collectors[genID] = c
	}
	return c
}

// deleteCollector 清理已终结生成的轨迹收集器，避免长期驻留内存。
func (s *Service) deleteCollector(genID string) {
	s.collectorMu.Lock()
	defer s.collectorMu.Unlock()
	delete(s.collectors, genID)
}

// buildExecutionContext 加载会话与渠道受众边界并构建受信任的 ExecutionContext。
func (s *Service) buildExecutionContext(ctx context.Context, tenantID, convID string, g *Generation) (*ExecutionContext, error) {
	var row struct {
		Kind      conversation.Kind `bun:"kind"`
		ChannelID *string           `bun:"channel_id"`
		ContactID *string           `bun:"contact_id"`
		Audience  string            `bun:"audience"`
	}
	if err := s.db.NewSelect().TableExpr("conversations AS c").
		Column("c.kind", "c.contact_id").
		ColumnExpr("COALESCE(c.reply_channel_id, c.channel_id) AS channel_id").
		ColumnExpr("COALESCE(ch.settings->>'audience', 'customer') AS audience").
		Join("LEFT JOIN channels AS ch ON ch.id = COALESCE(c.reply_channel_id, c.channel_id) AND ch.tenant_id = c.tenant_id").
		Where("c.id = ? AND c.tenant_id = ?", convID, tenantID).
		Scan(ctx, &row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, conversation.ErrNotFound.New()
		}
		return nil, errors.Wrap(err, "load execution conversation metadata")
	}
	targetLang, err := ResolveTargetLanguage(ctx, s.db, tenantID, convID, "")
	if err != nil {
		return nil, err
	}
	var chID, contactID string
	if row.ChannelID != nil {
		chID = *row.ChannelID
	}
	if row.ContactID != nil {
		contactID = *row.ContactID
	}
	audienceExternal := row.Kind == conversation.Service && row.Audience != "internal"
	return &ExecutionContext{
		TenantID:         tenantID,
		ConversationID:   convID,
		ConversationKind: row.Kind,
		ChannelID:        chID,
		ContactID:        contactID,
		AudienceExternal: audienceExternal,
		BotID:            g.ActorID,
		GenerationID:     g.ID,
		Mode:             g.Mode,
		Language:         targetLang,
		Collector:        s.collectorFor(g.ID),
	}, nil
}

// resolveRunSetup 加载当前对话的活跃生成、启用机器人及运行边界上下文，供 agentFor 与 approvalsFor 共用。
func (s *Service) resolveRunSetup(ctx context.Context, c chat.Conversation) (*Bot, BotSettings, *ExecutionContext, error) {
	var g Generation
	if err := s.db.NewSelect().Model(&g).
		Where("tenant_id = ? AND conversation_id = ? AND state IN (?)",
			c.UserID, c.ID, bun.In([]State{Pending, Streaming, WaitingApproval})).
		OrderExpr("created_at DESC").Limit(1).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, BotSettings{}, nil, ErrNotFound.New()
		}
		return nil, BotSettings{}, nil, errors.Wrap(err, "load active generation")
	}
	var bot Bot
	if err := s.db.NewSelect().Model(&bot).
		Where("id = ? AND tenant_id = ? AND enabled = true", g.ActorID, c.UserID).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, BotSettings{}, nil, ErrBotNotFound.New()
		}
		return nil, BotSettings{}, nil, errors.Wrap(err, "load bot for agent run")
	}
	cfg := bot.Config()
	exec, err := s.buildExecutionContext(ctx, c.UserID, c.ID, &g)
	if err != nil {
		return nil, BotSettings{}, nil, err
	}
	return &bot, cfg, exec, nil
}

// approvalsFor 按对话当前的机器人配置动态返回本轮需要人工审批确认的工具名称。
func (s *Service) approvalsFor(ctx context.Context, c chat.Conversation) ([]string, error) {
	_, cfg, exec, err := s.resolveRunSetup(ctx, c)
	if err != nil {
		return nil, err
	}
	_, approvals := s.registry.ToolsFor(s, cfg, exec)
	return approvals, nil
}

// agentFor 按对话当前的活跃生成、机器人配置与安全边界构建本轮执行的 llmagent。
func (s *Service) agentFor(ctx context.Context, c chat.Conversation) (agent.Agent, error) {
	bot, cfg, exec, err := s.resolveRunSetup(ctx, c)
	if err != nil {
		return nil, err
	}
	instr, targetLang, err := BuildInstruction(ctx, s.db, s.trSettings, cfg, exec)
	if err != nil {
		return nil, err
	}
	exec.Language = targetLang

	tools, _ := s.registry.ToolsFor(s, cfg, exec)
	m, err := s.modelFor(ctx, cfg)
	if err != nil {
		return nil, err
	}
	wrappedModel := &contextModel{inner: m, exec: exec}

	genCfg := model.GenerationConfig{Stream: true, Temperature: cfg.Temperature}
	return llmagent.New(bot.ID,
		llmagent.WithModel(wrappedModel),
		llmagent.WithInstruction(instr),
		llmagent.WithTools(tools),
		llmagent.WithMaxToolIterations(cfg.MaxToolIterations),
		llmagent.WithGenerationConfig(genCfg),
	), nil
}

// modelFor 返回底层推理模型：优先使用注入的测试替身，否则从平台网关配置创建。
func (s *Service) modelFor(ctx context.Context, cfg BotSettings) (model.Model, error) {
	if s.modelOverride != nil {
		return s.modelOverride, nil
	}
	if s.llm == nil {
		return nil, llm.ErrNotConfigured.New()
	}
	mCfg, hc, err := s.llm.ModelConfig(ctx, llm.Chat)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Model) != "" {
		mCfg.Model = strings.TrimSpace(cfg.Model)
	}
	return chat.NewModel(mCfg, openai.WithOpenAIOptions(openaiopt.WithHTTPClient(hc))), nil
}

// contextModel 在模型调用期间绑定会话粘性、生成归因和受信任运行上下文，并识别预算熔断错误。
type contextModel struct {
	inner model.Model
	exec  *ExecutionContext
}

func (m *contextModel) Info() model.Info { return m.inner.Info() }

func (m *contextModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if m.exec != nil {
		ctx = WithExecutionContext(ctx, m.exec)
		ctx = llm.WithSession(ctx, m.exec.ConversationID)
		ctx = llm.WithGeneration(ctx, m.exec.GenerationID)
		ctx = llm.WithTarget(ctx, llm.Target{
			Tenant:       m.exec.TenantID,
			GenerationID: m.exec.GenerationID,
			Type:         "conversation",
			ID:           m.exec.ConversationID,
			Operation:    "agent.chat",
		})
	}
	ch, err := m.inner.GenerateContent(ctx, req)
	if err != nil {
		if m.exec != nil && m.exec.Collector != nil && isBudgetExceededErr(err, "") {
			m.exec.Collector.SetBudgetExceeded(true)
		}
		return nil, err
	}
	out := make(chan *model.Response, 16)
	go func() {
		defer close(out)
		for rsp := range ch {
			if rsp != nil && rsp.Error != nil && m.exec != nil && m.exec.Collector != nil && isBudgetExceededErr(nil, rsp.Error.Message) {
				m.exec.Collector.SetBudgetExceeded(true)
			}
			out <- rsp
		}
	}()
	return out, nil
}

func isBudgetExceededErr(err error, msg string) bool {
	code := llm.ErrBudgetExceeded.Code()
	if err != nil {
		if errors.Is(err, llm.ErrBudgetExceeded) {
			return true
		}
		if strings.Contains(err.Error(), code) || strings.Contains(err.Error(), "budget_exceeded") {
			return true
		}
	}
	return strings.Contains(msg, code) || strings.Contains(msg, "budget_exceeded")
}

// consumeRun 消费 chat.Run 的事件流并驱动 generations 与 conversation.handling_mode 状态流转。
func (s *Service) consumeRun(ctx context.Context, g *Generation, run *chat.Run) error {
	if _, err := s.db.NewRaw(`UPDATE generations SET run_id = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
		run.ID, now(), g.ID, g.TenantID).Exec(ctx); err != nil {
		return errors.Wrap(err, "record generation run_id")
	}

	sys := identity.Subject{Type: identity.System, ID: "agent", Tenant: g.TenantID}
	var (
		textBuf   strings.Builder
		runErrStr string
		doneEv    *chat.Done
	)
	if initial := g.text(); initial != "" {
		textBuf.WriteString(initial)
	}

	for ev := range run.Events(ctx, "") {
		switch ev.Name {
		case chat.EventText:
			t, _ := ev.Data.(chat.Text)
			if t.Text != "" {
				if _, err := s.append.Handle(ctx, sys, AppendParams{GenerationID: g.ID, Delta: t.Text}); err != nil {
					if !errors.Is(err, ErrFinished) && !errors.Is(err, context.Canceled) {
						return err
					}
				}
				textBuf.WriteString(t.Text)
			}
		case chat.EventError:
			e, _ := ev.Data.(chat.Error)
			runErrStr = e.Message
		case chat.EventDone:
			d, _ := ev.Data.(chat.Done)
			doneEv = &d
		}
	}

	if ctx.Err() != nil && doneEv == nil {
		return nil
	}

	collector := s.collectorFor(g.ID)
	toolCalls := collector.ToolCalls()
	citations := collector.Citations()
	handoverReason := collector.HandoverReason()
	budgetExceeded := collector.BudgetExceeded() || isBudgetExceededErr(nil, runErrStr)

	if doneEv != nil && doneEv.Canceled {
		s.deleteCollector(g.ID)
		err := s.cancel.HandleWithReason(context.WithoutCancel(ctx), sys, g.ID, "barge_in")
		if errors.Is(err, ErrFinished) {
			return nil
		}
		return err
	}

	if doneEv != nil && len(doneEv.Pending) > 0 {
		approvals := make([]ToolApproval, len(doneEv.Pending))
		for i, tc := range doneEv.Pending {
			approvals[i] = ToolApproval{
				CallID:    tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
				Status:    "pending",
			}
		}
		return s.enterWaitingApproval(context.WithoutCancel(ctx), g, approvals, toolCalls, citations)
	}

	defer s.deleteCollector(g.ID)

	if runErrStr != "" {
		reason := ""
		if budgetExceeded {
			reason = "budget_exceeded"
		}
		err := s.fail.Handle(context.WithoutCancel(ctx), sys, FailParams{
			GenerationID:   g.ID,
			Reason:         runErrStr,
			ToolCalls:      toolCalls,
			Citations:      citations,
			Down:           s.down,
			HandoverReason: reason,
		})
		if errors.Is(err, ErrFinished) {
			return nil
		}
		return err
	}

	replyText := strings.TrimSpace(textBuf.String())
	if replyText == "" && handoverReason != "" {
		replyText = "我将为您转接人工客服继续处理。"
	}
	if replyText == "" {
		err := s.fail.Handle(context.WithoutCancel(ctx), sys, FailParams{
			GenerationID: g.ID,
			Reason:       "empty reply",
			ToolCalls:    toolCalls,
			Citations:    citations,
		})
		if errors.Is(err, ErrFinished) {
			return nil
		}
		return err
	}

	var usageMap map[string]any
	if doneEv != nil && doneEv.Usage != nil {
		usageMap = map[string]any{
			"prompt_tokens":     doneEv.Usage.PromptTokens,
			"cached_tokens":     doneEv.Usage.CachedTokens,
			"completion_tokens": doneEv.Usage.CompletionTokens,
			"reasoning_tokens":  doneEv.Usage.ReasoningTokens,
		}
	}
	_, err := s.finalize.Handle(context.WithoutCancel(ctx), sys, FinalizeParams{
		GenerationID:   g.ID,
		Text:           replyText,
		Usage:          usageMap,
		ToolCalls:      toolCalls,
		Citations:      citations,
		HandoverReason: handoverReason,
	})
	if errors.Is(err, ErrFinished) || errors.Is(err, ErrEmptyReply) {
		return nil
	}
	return err
}

// enterWaitingApproval 将生成与会话原子迁移到等待工具审批状态，并通知实时订阅者。
func (s *Service) enterWaitingApproval(ctx context.Context, g *Generation, approvals []ToolApproval, toolCalls []ToolCallRecord, citations []Citation) error {
	var updated Generation
	err := database.RunInTx(ctx, s.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := tx.NewSelect().Model(&updated).Where("id = ? AND tenant_id = ?", g.ID, g.TenantID).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return errors.Wrap(err, "lock generation for approval")
		}
		if !updated.State.active() {
			return ErrFinished.New()
		}
		updated.State = WaitingApproval
		updated.PendingApprovals = approvals
		if toolCalls != nil {
			updated.ToolCalls = toolCalls
		}
		if citations != nil {
			updated.Citations = citations
		}
		updated.Checkpoint++
		updated.UpdatedAt = now()
		if _, err := tx.NewUpdate().Model(&updated).
			Column("state", "pending_approvals", "tool_calls", "citations", "checkpoint_seq", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save waiting_approval generation")
		}
		return conversation.SetHandlingModeInTx(ctx, tx, s.down, g.TenantID, g.ConversationID, conversation.HandlingWaitingApproval, "", false)
	})
	if err != nil {
		if errors.Is(err, ErrFinished) {
			return nil
		}
		return err
	}
	if s.hub != nil {
		frame := Frame{
			Type: "approval_required", GenerationID: updated.ID, ConversationID: updated.ConversationID, Mode: updated.Mode,
			Checkpoint: updated.Checkpoint, State: updated.State, PendingApprovals: updated.PendingApprovals, Citations: updated.Citations,
		}
		if err := s.hub.PublishJSON(ctx, Channel(updated.TenantID, updated.ID), frame); err != nil {
			return errors.Wrap(err, "publish approval_required frame")
		}
	}
	return nil
}
