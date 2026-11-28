package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"agents-desk/internal/conversation"
)

// RiskLevel 定义工具的风险等级，决定其在外部自动接待中的开放与人工审批要求。
type RiskLevel string

const (
	// RiskReadOnly 只读上下文或对外可见知识。
	RiskReadOnly RiskLevel = "read_only"
	// RiskDraftOnly 仅产生草稿或建议，无外部状态变更。
	RiskDraftOnly RiskLevel = "draft_only"
	// RiskLowImpact 低风险轻量动作（例如触发转人工）。
	RiskLowImpact RiskLevel = "low_impact"
	// RiskHighImpact 高风险副作用动作（如退款、修改订单、取消订阅），调用前强制人工审批。
	RiskHighImpact RiskLevel = "high_impact"
)

// ToolSpec 描述一个可注册给 AI Agent 使用的工具及其安全边界元数据。
type ToolSpec struct {
	Name            string
	Description     string
	Risk            RiskLevel
	ExternalAllowed bool
	Build           func(svc *Service) tool.CallableTool
}

// Registry 维护供 Service 装配的工具目录，支持内置安全工具与后续业务扩展工具。
type Registry struct {
	mu    sync.RWMutex
	specs map[string]ToolSpec
	order []string
}

// NewRegistry 创建工具注册表并注册默认安全工具集。
func NewRegistry() *Registry {
	r := &Registry{specs: map[string]ToolSpec{}}
	r.Register(ToolSpec{
		Name:            "get_conversation_context",
		Description:     "Read sanitized metadata for the current conversation, contact, and handling mode.",
		Risk:            RiskReadOnly,
		ExternalAllowed: true,
		Build:           func(svc *Service) tool.CallableTool { return svc.buildContextTool() },
	})
	r.Register(ToolSpec{
		Name:            "search_knowledge",
		Description:     "Search tenant knowledge base with automatic external/internal visibility isolation.",
		Risk:            RiskReadOnly,
		ExternalAllowed: true,
		Build:           func(svc *Service) tool.CallableTool { return svc.buildSearchKnowledgeTool() },
	})
	r.Register(ToolSpec{
		Name:            "handover_to_human",
		Description:     "Escalate and hand over this conversation to a human support agent with a concise reason.",
		Risk:            RiskLowImpact,
		ExternalAllowed: true,
		Build:           func(_ *Service) tool.CallableTool { return buildHandoverTool() },
	})
	return r
}

// Register 注册或覆盖一个工具定义。
func (r *Registry) Register(spec ToolSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[spec.Name]; !exists {
		r.order = append(r.order, spec.Name)
	}
	r.specs[spec.Name] = spec
}

// ToolsFor 按机器人白名单、会话内外部边界以及生成模式装配允许使用的工具及强制审批工具名列表。
func (r *Registry) ToolsFor(svc *Service, bot BotSettings, exec *ExecutionContext) ([]tool.Tool, []string) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var (
		tools         []tool.Tool
		approvalNames []string
	)
	hasWhitelist := len(bot.AllowedTools) > 0
	for _, name := range r.order {
		spec := r.specs[name]
		if hasWhitelist && !slices.Contains(bot.AllowedTools, name) {
			continue
		}
		// 对外客户接待时，非显式白名单的高风险工具或内部专用工具一律拦截。
		if exec.AudienceExternal && exec.Mode == ModeReply && !spec.ExternalAllowed && spec.Risk != RiskHighImpact {
			continue
		}
		if exec.AudienceExternal && !spec.ExternalAllowed && spec.Risk == RiskHighImpact && !hasWhitelist {
			continue
		}
		raw := spec.Build(svc)
		wrapped := &safeTool{spec: spec, inner: raw, exec: exec}
		tools = append(tools, wrapped)

		if spec.Risk == RiskHighImpact || slices.Contains(bot.RequireApprovalTools, name) {
			approvalNames = append(approvalNames, name)
		}
	}
	return tools, approvalNames
}

// AllApprovalCandidates 返回注册表中所有可能成为高风险的工具名，供启动阶段登记占位检查。
func (r *Registry) AllApprovalCandidates() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for _, name := range r.order {
		if r.specs[name].Risk == RiskHighImpact {
			names = append(names, name)
		}
	}
	return names
}

// safeTool 是所有暴露给模型的工具统一安全包装：强制要求受信任 ExecutionContext，
// 并把耗时、结果摘要与错误审计写入 TraceCollector。
type safeTool struct {
	spec  ToolSpec
	inner tool.CallableTool
	exec  *ExecutionContext
}

// Declaration 返回底层工具声明。
func (w *safeTool) Declaration() *tool.Declaration {
	return w.inner.Declaration()
}

// Call 执行工具并强制注入/校验受信任执行上下文，同时记录审计日志。
func (w *safeTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	exec, ok := ExecutionContextFrom(ctx)
	if !ok && w.exec != nil {
		exec = w.exec
		ctx = WithExecutionContext(ctx, exec)
		ok = true
	}
	if !ok {
		return nil, errors.New("agent: missing trusted execution context")
	}
	started := time.Now()
	out, err := w.inner.Call(ctx, jsonArgs)
	durationMS := time.Since(started).Milliseconds()
	rec := ToolCallRecord{
		CallID:     w.spec.Name + ":" + started.UTC().Format("150405.000"),
		Name:       w.spec.Name,
		DurationMS: durationMS,
	}
	if err != nil {
		rec.Error = err.Error()
	} else {
		raw, _ := json.Marshal(out)
		rec.ResultSummary = truncateSummary(string(raw), 240)
	}
	if exec.Collector != nil {
		exec.Collector.AddToolCall(rec)
	}
	return out, err
}

// truncateSummary 把审计摘要限制在 maxRunes 个字符以内。
func truncateSummary(s string, maxRunes int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxRunes {
		return string(r)
	}
	return string(r[:maxRunes]) + "…"
}

type contextToolInput struct{}

type contextToolOutput struct {
	ConversationKind string `json:"conversation_kind"`
	HandlingMode     string `json:"handling_mode"`
	Language         string `json:"language"`
	ContactName      string `json:"contact_name,omitempty"`
	Subject          string `json:"subject,omitempty"`
	ChannelName      string `json:"channel_name,omitempty"`
	ChannelKind      string `json:"channel_kind,omitempty"`
}

func (s *Service) buildContextTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ contextToolInput) (contextToolOutput, error) {
			exec, ok := ExecutionContextFrom(ctx)
			if !ok {
				return contextToolOutput{}, errors.New("agent: missing execution context")
			}
			var row struct {
				Kind         string  `bun:"kind"`
				HandlingMode string  `bun:"handling_mode"`
				Language     string  `bun:"language"`
				Subject      string  `bun:"subject"`
				ContactName  *string `bun:"contact_name"`
				ChannelName  *string `bun:"channel_name"`
				ChannelKind  *string `bun:"channel_kind"`
			}
			err := s.db.NewSelect().TableExpr("conversations AS c").
				Column("c.kind", "c.handling_mode", "c.language", "c.subject").
				ColumnExpr("ct.name AS contact_name").
				ColumnExpr("ch.name AS channel_name").
				ColumnExpr("ch.kind AS channel_kind").
				Join("LEFT JOIN contacts AS ct ON ct.id = c.contact_id AND ct.tenant_id = c.tenant_id").
				Join("LEFT JOIN channels AS ch ON ch.id = COALESCE(c.reply_channel_id, c.channel_id) AND ch.tenant_id = c.tenant_id").
				Where("c.id = ? AND c.tenant_id = ?", exec.ConversationID, exec.TenantID).
				Scan(ctx, &row)
			if err != nil {
				return contextToolOutput{}, errors.Wrap(err, "load conversation context")
			}
			out := contextToolOutput{
				ConversationKind: row.Kind,
				HandlingMode:     row.HandlingMode,
				Language:         exec.Language,
				Subject:          row.Subject,
			}
			if row.Language != "" && row.Language != conversation.Undetermined {
				out.Language = row.Language
			}
			if row.ContactName != nil {
				out.ContactName = *row.ContactName
			}
			if row.ChannelName != nil {
				out.ChannelName = *row.ChannelName
			}
			if row.ChannelKind != nil {
				out.ChannelKind = *row.ChannelKind
			}
			return out, nil
		},
		function.WithName("get_conversation_context"),
		function.WithDescription("Read trusted context metadata for the active conversation"),
	)
}

type searchKnowledgeInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type searchKnowledgeOutput struct {
	Citations []Citation `json:"citations"`
}

func (s *Service) buildSearchKnowledgeTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, in searchKnowledgeInput) (searchKnowledgeOutput, error) {
			exec, ok := ExecutionContextFrom(ctx)
			if !ok {
				return searchKnowledgeOutput{}, errors.New("agent: missing execution context")
			}
			limit := in.Limit
			if limit <= 0 || limit > 8 {
				limit = 5
			}
			vis := KnowledgeInternal
			if exec.AudienceExternal {
				vis = KnowledgeExternal
			}
			cites, err := s.retriever.Retrieve(ctx, RetrieveRequest{
				TenantID:   exec.TenantID,
				Query:      strings.TrimSpace(in.Query),
				Language:   exec.Language,
				Visibility: vis,
				ChannelID:  exec.ChannelID,
				Limit:      limit,
			})
			if err != nil {
				return searchKnowledgeOutput{}, errors.Wrap(err, "retrieve knowledge")
			}
			// 双重防御：外部会话结果过滤掉任何非 external 可见性的切片
			if exec.AudienceExternal {
				filtered := make([]Citation, 0, len(cites))
				for _, c := range cites {
					if c.Visibility == KnowledgeExternal || c.Visibility == "" {
						c.Visibility = KnowledgeExternal
						filtered = append(filtered, c)
					}
				}
				cites = filtered
			}
			if exec.Collector != nil && len(cites) > 0 {
				exec.Collector.AddCitations(cites)
			}
			return searchKnowledgeOutput{Citations: cites}, nil
		},
		function.WithName("search_knowledge"),
		function.WithDescription("Search workspace knowledge base scoped to conversation visibility"),
	)
}

type handoverInput struct {
	Reason string `json:"reason"`
}

type handoverOutput struct {
	HandoverRequested bool   `json:"handover_requested"`
	Reason            string `json:"reason"`
}

func buildHandoverTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, in handoverInput) (handoverOutput, error) {
			exec, ok := ExecutionContextFrom(ctx)
			if !ok {
				return handoverOutput{}, errors.New("agent: missing execution context")
			}
			reason := strings.TrimSpace(in.Reason)
			if reason == "" {
				reason = "ai_requested"
			}
			if exec.Collector != nil {
				exec.Collector.RequestHandover(reason)
			}
			return handoverOutput{HandoverRequested: true, Reason: reason}, nil
		},
		function.WithName("handover_to_human"),
		function.WithDescription("Hand over the current conversation to a human support agent"),
	)
}
