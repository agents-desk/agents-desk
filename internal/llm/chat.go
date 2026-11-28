package llm

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/openai"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/settings"
)

var (
	// ErrNotConfigured 是网关或这种调用类型的模型名还没有配置：平台管理员在管理员端的平台设置中填写。
	ErrNotConfigured = errorx.Define(errorx.Conflict, "llm.not_configured")
	// ErrFailed 是模型接口调用失败，或者回答不是要求的格式。
	ErrFailed = errorx.Define(errorx.Unavailable, "llm.failed")
)

// Message 是对话中的一条消息。
type Message struct {
	Role    string `json:"role"` // system、user、assistant
	Content string `json:"content"`
}

// JSONRequest 是要求按 JSON Schema 回答的一次无状态补全调用。
type JSONRequest struct {
	Messages    []Message
	SchemaName  string         // Schema 的名字，例如 translation
	Schema      map[string]any // 回答的 JSON Schema
	NoReasoning bool           // 设为 true 时向网关发送标准 reasoning_effort="none" 关闭思考
}

// Target 是一次调用的业务归因，写入 llm_calls：工作区、生成 ID、业务场景名（例如 incoming、reply）、
// 结构化元数据，以及关联对象的类型和 ID（例如 timeline_item 和它的 ID）。
type Target struct {
	Tenant       string
	GenerationID string
	Type         string
	ID           string
	Operation    string
	Meta         map[string]any
}

// Call 是一次调用记录，对应 llm_calls 表。
type Call struct {
	bun.BaseModel `bun:"table:llm_calls"`

	ID               string         `bun:"id,pk"`
	CreatedAt        time.Time      `bun:"created_at,notnull"`
	UpdatedAt        time.Time      `bun:"updated_at,notnull"`
	TenantID         *string        `bun:"tenant_id"`
	GenerationID     *string        `bun:"generation_id"` // 关联的 AI 生成记录 ID；非 Agent 生成调用为空
	CallType         CallType       `bun:"call_type,notnull"`
	Operation        string         `bun:"operation,notnull"` // 业务场景名，例如 incoming、fill、reply、back_translate
	Meta             map[string]any `bun:"meta,type:jsonb,notnull"`
	Endpoint         string         `bun:"endpoint,notnull"` // 网关地址，不含密钥
	Model            string         `bun:"model,notnull"`
	RequestID        string         `bun:"request_id,notnull"` // 网关返回的请求 ID（X-Omirouter-Request-Id）
	Request          any            `bun:"request,type:jsonb,notnull"`
	Response         string         `bun:"response,notnull"`
	PromptTokens     int            `bun:"prompt_tokens,notnull"`
	CachedTokens     int            `bun:"cached_tokens,notnull"`     // 缓存命中的输入 token
	CompletionTokens int            `bun:"completion_tokens,notnull"` // 包括思考用的 ReasoningTokens
	ReasoningTokens  int            `bun:"reasoning_tokens,notnull"`  // 思考用的 token；关闭思考时为 0
	Cost             *float64       `bun:"cost"`                      // 网关或供应商返回的费用（美元），没有时为空
	DurationMS       int            `bun:"duration_ms,notnull"`
	Error            string         `bun:"error,notnull"`
	TargetType       string         `bun:"target_type,notnull"`
	TargetID         string         `bun:"target_id,notnull"`
}

// contextKey 是本包在 context 中传递归因、会话 ID、生成 ID 与调用 ID 槽位的私有键。
type (
	targetCtxKey     struct{}
	sessionCtxKey    struct{}
	generationCtxKey struct{}
	callIDCtxKey     struct{}
)

// WithTarget 返回携带业务归因信息的子上下文，供统一观测传输层写入 llm_calls。
func WithTarget(ctx context.Context, target Target) context.Context {
	return context.WithValue(ctx, targetCtxKey{}, target)
}

// WithSession 返回携带网关会话粘性 ID 的子上下文；观测传输层会将其写入 X-Omirouter-Session 请求头。
func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

// SessionFromContext 从上下文中取出网关会话粘性 ID；未设置时返回空字符串。
func SessionFromContext(ctx context.Context) string {
	s, _ := ctx.Value(sessionCtxKey{}).(string)
	return s
}

// WithGeneration 返回携带关联 AI 生成 ID 的子上下文，供统一观测传输层归因记录 llm_calls.generation_id。
func WithGeneration(ctx context.Context, generationID string) context.Context {
	return context.WithValue(ctx, generationCtxKey{}, generationID)
}

// GenerationFromContext 从上下文中取出关联的 AI 生成 ID；未设置时返回空字符串。
func GenerationFromContext(ctx context.Context) string {
	s, _ := ctx.Value(generationCtxKey{}).(string)
	return s
}

// Client 通过 go-tenon/tenon/ai 调用平台设置的统一模型网关，并由统一的 http.RoundTripper 执行预算熔断与调用记录。可以并发使用。
type Client struct {
	db           bun.IDB
	settings     *settings.Group[Settings]
	tenantBudget *settings.Group[TenantBudget]
	http         *http.Client
}

// NewClient 返回模型调用客户端；s 是平台的模型网关设置，h 提供基础传输与超时控制（为空时使用默认超时客户端）。
func NewClient(db bun.IDB, s *settings.Group[Settings], h *http.Client) *Client {
	var tb *settings.Group[TenantBudget]
	if db != nil {
		tb = NewTenantBudgetGroup(db)
	}
	return &Client{db: db, settings: s, tenantBudget: tb, http: h}
}

// Configured 返回调用类型 t 是否已配置（网关地址和该调用类型的逻辑模型名都已填写）。
func (c *Client) Configured(ctx context.Context, t CallType) (bool, error) {
	s, err := c.settings.Get(ctx)
	if err != nil {
		return false, err
	}
	return s.Gateway.BaseURL != "" && s.model(t) != "", nil
}

// ModelConfig 按平台设置为调用类型 t 返回 ai.ModelConfig 与绑定了预算熔断、会话粘性与调用记录的 *http.Client，
// 供直接构造 openai.New 或 ai/chat.NewModel 使用。未配置时返回 ErrNotConfigured。
func (c *Client) ModelConfig(ctx context.Context, t CallType) (ai.ModelConfig, *http.Client, error) {
	s, err := c.settings.Get(ctx)
	if err != nil {
		return ai.ModelConfig{}, nil, err
	}
	modelName := s.model(t)
	if s.Gateway.BaseURL == "" || modelName == "" {
		return ai.ModelConfig{}, nil, ErrNotConfigured.New()
	}
	cfg := ai.ModelConfig{
		Model:      modelName,
		BaseURL:    s.Gateway.BaseURL,
		APIKey:     s.Gateway.APIKey,
		MaxRetries: -1, // 多上游故障转移与重试由统一模型网关负责
	}
	return cfg, c.observedHTTPClient(t, s.Gateway.BaseURL, modelName), nil
}

// OpenAI 按平台设置为调用类型 t 创建绑定了预算熔断与统一观测传输层的 tenon/ai/openai 客户端。
// 网关地址或该调用类型的逻辑模型名为空时返回 ErrNotConfigured。
func (c *Client) OpenAI(ctx context.Context, t CallType) (*openai.Client, error) {
	cfg, hc, err := c.ModelConfig(ctx, t)
	if err != nil {
		return nil, err
	}
	return openai.New(cfg, openai.WithHTTPClient(hc)), nil
}

// JSON 用调用类型 t 对应的逻辑模型发出标准结构化 JSON 补全请求 req，并将结果解码到 out。
// 未配置时返回 ErrNotConfigured，超工作区预算时返回 ErrBudgetExceeded，调用失败或回答不符合 JSON 格式时返回 ErrFailed。
func (c *Client) JSON(ctx context.Context, t CallType, target Target, req JSONRequest, out any) error {
	oa, err := c.OpenAI(ctx, t)
	if err != nil {
		return err
	}
	msgs := make([]ai.Message, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = ai.Message{Role: ai.Role(m.Role), Content: m.Content}
	}
	temp := 0.2
	creq := ai.CompleteRequest{
		Messages:    msgs,
		Temperature: &temp,
		JSONSchema: &ai.JSONSchema{
			Name:   req.SchemaName,
			Strict: true,
			Schema: req.Schema,
		},
	}
	if req.NoReasoning {
		creq.ReasoningEffort = "none"
	}

	var callID string
	callCtx := WithTarget(ctx, target)
	callCtx = context.WithValue(callCtx, callIDCtxKey{}, &callID)

	raw, err := ai.CompleteJSON[json.RawMessage](callCtx, oa, creq)
	if err == nil {
		err = json.Unmarshal(raw, out)
	}
	if err != nil {
		if errors.Is(err, ErrBudgetExceeded) {
			return ErrBudgetExceeded.New()
		}
		// 若传输层已记录成功响应但 JSON 解码失败，将解码错误补充回同一条 llm_calls 记录。
		if callID != "" && c.db != nil {
			_, _ = c.db.NewUpdate().Model((*Call)(nil)).
				Set("error = ?, updated_at = ?", err.Error(), time.Now().UTC().Truncate(time.Microsecond)).
				Where("id = ? AND error = ''", callID).
				Exec(context.WithoutCancel(ctx))
		}
		return ErrFailed.Wrap(err)
	}
	return nil
}

// observedHTTPClient 返回包装了预算校验、会话粘性头和 ai.ObserveTransport 的 *http.Client，
// 保留基础客户端的底层 Transport 与超时策略（不做硬编码超时截断，以支持流式长响应）。
func (c *Client) observedHTTPClient(t CallType, endpoint, modelName string) *http.Client {
	base := http.DefaultTransport
	var timeout time.Duration
	if c.http != nil {
		if c.http.Transport != nil {
			base = c.http.Transport
		}
		timeout = c.http.Timeout
	}
	observed := ai.ObserveTransport(sessionTransport{base: base}, func(ctx context.Context, ev ai.CallEvent) {
		c.recordCall(ctx, t, endpoint, modelName, ev)
	}, ai.ObserveOptions{MaxBodyBytes: 4 << 20})
	return &http.Client{
		Timeout:   timeout,
		Transport: budgetTransport{client: c, next: observed},
	}
}

// sessionTransport 在发出模型请求前把上下文中的网关会话粘性 ID 写入 X-Omirouter-Session 请求头。
type sessionTransport struct {
	base http.RoundTripper
}

// RoundTrip 实现 http.RoundTripper，透传会话粘性头。
func (t sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if sid := SessionFromContext(req.Context()); sid != "" && req.Header.Get("X-Omirouter-Session") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("X-Omirouter-Session", sid)
	}
	return t.base.RoundTrip(req)
}

// recordCall 将 ai.ObserveTransport 采集的非流式或流式 SSE 模型调用写入 llm_calls。
// 优先使用 OmiRouter 统一计量响应头，缺失时回退使用 CallEvent 解析出的标准用量；落库失败只记错误日志，不丢弃已返回的模型响应。
func (c *Client) recordCall(ctx context.Context, callType CallType, endpoint, modelName string, ev ai.CallEvent) {
	var reqJSON any = map[string]any{}
	if len(ev.RequestBody) > 0 {
		var obj map[string]any
		if json.Unmarshal(ev.RequestBody, &obj) == nil && obj != nil {
			reqJSON = obj
		}
	}
	target, _ := ctx.Value(targetCtxKey{}).(Target)
	meta := target.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	model := modelName
	if model == "" {
		model = ev.Model
	}
	genID := target.GenerationID
	if genID == "" {
		genID = GenerationFromContext(ctx)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	call := &Call{
		ID:               uuid.Must(uuid.NewV7()).String(),
		CreatedAt:        now,
		UpdatedAt:        now,
		CallType:         callType,
		Operation:        target.Operation,
		Meta:             meta,
		Endpoint:         endpoint,
		Model:            model,
		RequestID:        ev.Headers.Get("X-Omirouter-Request-Id"),
		Request:          reqJSON,
		Response:         ev.Content,
		PromptTokens:     ev.PromptTokens,
		CachedTokens:     ev.CachedTokens,
		CompletionTokens: ev.CompletionTokens,
		ReasoningTokens:  ev.ReasoningTokens,
		DurationMS:       int(ev.Duration.Milliseconds()),
		TargetType:       target.Type,
		TargetID:         target.ID,
	}
	if target.Tenant != "" {
		call.TenantID = &target.Tenant
	}
	if genID != "" {
		call.GenerationID = &genID
	}
	if slot, ok := ctx.Value(callIDCtxKey{}).(*string); ok && slot != nil {
		*slot = call.ID
	}
	if call.Response == "" && len(ev.ResponseBody) > 0 {
		call.Response = string(ev.ResponseBody)
	}
	if ev.Err != nil {
		call.Error = ev.Err.Error()
	}

	// 优先读取 OmiRouter 统一计量响应头覆盖用量和费用。
	if ev.Headers != nil {
		if v, ok := parseHeaderInt(ev.Headers.Get("X-Omirouter-Prompt-Tokens")); ok {
			call.PromptTokens = v
		}
		if v, ok := parseHeaderInt(ev.Headers.Get("X-Omirouter-Cached-Tokens")); ok {
			call.CachedTokens = v
		}
		if v, ok := parseHeaderInt(ev.Headers.Get("X-Omirouter-Completion-Tokens")); ok {
			call.CompletionTokens = v
		}
		if v, ok := parseHeaderInt(ev.Headers.Get("X-Omirouter-Reasoning-Tokens")); ok {
			call.ReasoningTokens = v
		}
		if rawCost := strings.TrimSpace(ev.Headers.Get("X-Omirouter-Cost")); rawCost != "" {
			if cost, err := strconv.ParseFloat(rawCost, 64); err == nil {
				call.Cost = &cost
			}
		}
	}
	if call.Cost == nil && len(ev.ResponseBody) > 0 {
		var costEnv struct {
			Usage struct {
				Cost *float64 `json:"cost"`
			} `json:"usage"`
		}
		if json.Unmarshal(ev.ResponseBody, &costEnv) == nil && costEnv.Usage.Cost != nil {
			call.Cost = costEnv.Usage.Cost
		}
	}

	if _, err := c.db.NewInsert().Model(call).Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "record llm call failed", "call_id", call.ID, "operation", call.Operation, "error", err)
	}
}

// parseHeaderInt 解析非空整数响应头。
func parseHeaderInt(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}
