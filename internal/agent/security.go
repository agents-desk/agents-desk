package agent

import (
	"context"
	"sync"

	"agents-desk/internal/conversation"
)

// executionCtxKey 是在 context 中传递受信任运行上下文的私有键。
type executionCtxKey struct{}

// TraceCollector 在单次 AI 运行期间线程安全地累积工具调用、检索引用、转人工意愿与预算熔断状态。
type TraceCollector struct {
	mu             sync.Mutex
	toolCalls      []ToolCallRecord
	citations      []Citation
	handoverReason string
	budgetExceeded bool
}

// NewTraceCollector 创建一次运行的轨迹收集器。
func NewTraceCollector() *TraceCollector {
	return &TraceCollector{}
}

// AddToolCall 记录一条工具调用审计摘要。
func (c *TraceCollector) AddToolCall(rec ToolCallRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.toolCalls = append(c.toolCalls, rec)
}

// ToolCalls 返回已记录的全部工具调用副本。
func (c *TraceCollector) ToolCalls() []ToolCallRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ToolCallRecord, len(c.toolCalls))
	copy(out, c.toolCalls)
	return out
}

// AddCitations 追加检索命中的知识库引用（按 chunk_id 去重）。
func (c *TraceCollector) AddCitations(list []Citation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]bool, len(c.citations))
	for _, existing := range c.citations {
		seen[existing.ChunkID] = true
	}
	for _, item := range list {
		if item.ChunkID != "" && seen[item.ChunkID] {
			continue
		}
		if item.ChunkID != "" {
			seen[item.ChunkID] = true
		}
		c.citations = append(c.citations, item)
	}
}

// Citations 返回全部去重后的检索引用副本。
func (c *TraceCollector) Citations() []Citation {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Citation, len(c.citations))
	copy(out, c.citations)
	return out
}

// RequestHandover 标记模型通过工具主动请求转交人工坐席。
func (c *TraceCollector) RequestHandover(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if reason == "" {
		reason = "ai_requested"
	}
	c.handoverReason = reason
}

// HandoverReason 返回转交人工的原因，空字符串表示未触发。
func (c *TraceCollector) HandoverReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handoverReason
}

// SetBudgetExceeded 记录本次运行是否命中了工作区模型预算上限。
func (c *TraceCollector) SetBudgetExceeded(exceeded bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.budgetExceeded = exceeded
}

// BudgetExceeded 报告本次运行是否因工作区模型预算上限而熔断。
func (c *TraceCollector) BudgetExceeded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.budgetExceeded
}

// ExecutionContext 保存由服务端注入的受信任运行边界，工具只能从中读取租户和会话标识，
// 不接受模型传入任何 tenant_id 或 conversation_id 参数以越权访问。
type ExecutionContext struct {
	TenantID         string
	ConversationID   string
	ConversationKind conversation.Kind
	ChannelID        string
	ContactID        string
	AudienceExternal bool // 是否面向外部客户接待，决定知识库检索与上下文可见边界
	BotID            string
	GenerationID     string
	Mode             Mode
	Language         string
	Collector        *TraceCollector
}

// WithExecutionContext 把受信任运行上下文注入 ctx。
func WithExecutionContext(ctx context.Context, exec *ExecutionContext) context.Context {
	return context.WithValue(ctx, executionCtxKey{}, exec)
}

// ExecutionContextFrom 从 ctx 中取出受信任运行上下文。
func ExecutionContextFrom(ctx context.Context) (*ExecutionContext, bool) {
	exec, ok := ctx.Value(executionCtxKey{}).(*ExecutionContext)
	return exec, ok && exec != nil
}
