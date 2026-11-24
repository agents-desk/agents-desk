package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

// Target 是一次调用的业务归因，写入 llm_calls：工作区、业务场景名（例如 incoming、reply）、
// 结构化元数据，以及关联对象的类型和 ID（例如 timeline_item 和它的 ID）。
type Target struct {
	Tenant    string
	Type      string
	ID        string
	Operation string
	Meta      map[string]any
}

// Call 是一次调用记录，对应 llm_calls 表。
type Call struct {
	bun.BaseModel `bun:"table:llm_calls"`

	ID               string         `bun:"id,pk"`
	CreatedAt        time.Time      `bun:"created_at,notnull"`
	UpdatedAt        time.Time      `bun:"updated_at,notnull"`
	TenantID         *string        `bun:"tenant_id"`
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

// contextKey 是本包在 context 中传递归因、会话 ID 与调用 ID 槽位的私有键。
type (
	targetCtxKey  struct{}
	sessionCtxKey struct{}
	callIDCtxKey  struct{}
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

// Client 通过 go-tenon/tenon/ai 调用平台设置的统一模型网关，并由统一的 http.RoundTripper 记录每次调用。可以并发使用。
type Client struct {
	db       bun.IDB
	settings *settings.Group[Settings]
	http     *http.Client
}

// NewClient 返回模型调用客户端；s 是平台的模型网关设置，h 提供基础传输与超时控制（为空时使用默认超时客户端）。
func NewClient(db bun.IDB, s *settings.Group[Settings], h *http.Client) *Client {
	return &Client{db: db, settings: s, http: h}
}

// Configured 返回调用类型 t 是否已配置（网关地址和该调用类型的逻辑模型名都已填写）。
func (c *Client) Configured(ctx context.Context, t CallType) (bool, error) {
	s, err := c.settings.Get(ctx)
	if err != nil {
		return false, err
	}
	return s.Gateway.BaseURL != "" && s.model(t) != "", nil
}

// OpenAI 按平台设置为调用类型 t 创建绑定了统一观测传输层的 tenon/ai/openai 客户端。
// 网关地址或该调用类型的逻辑模型名为空时返回 ErrNotConfigured。
func (c *Client) OpenAI(ctx context.Context, t CallType) (*openai.Client, error) {
	s, err := c.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	modelName := s.model(t)
	if s.Gateway.BaseURL == "" || modelName == "" {
		return nil, ErrNotConfigured.New()
	}
	return openai.New(
		ai.ModelConfig{
			Model:      modelName,
			BaseURL:    s.Gateway.BaseURL,
			APIKey:     s.Gateway.APIKey,
			MaxRetries: -1, // 多上游故障转移与重试由统一模型网关负责
		},
		openai.WithHTTPClient(c.observedHTTPClient(t, s.Gateway.BaseURL, modelName)),
	), nil
}

// JSON 用调用类型 t 对应的逻辑模型发出标准结构化 JSON 补全请求 req，并将结果解码到 out。
// 未配置时返回 ErrNotConfigured，调用失败或回答不符合 JSON 格式时返回 ErrFailed。
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
		// 若传输层已记录成功响应但 JSON 解码失败，将解码错误补充回同一条 llm_calls 记录。
		if callID != "" {
			_, _ = c.db.NewUpdate().Model((*Call)(nil)).
				Set("error = ?, updated_at = ?", err.Error(), time.Now().UTC().Truncate(time.Microsecond)).
				Where("id = ? AND error = ''", callID).
				Exec(context.WithoutCancel(ctx))
		}
		return ErrFailed.Wrap(err)
	}
	return nil
}

// observedHTTPClient 返回包装了统一观测传输层的 *http.Client，保留基础客户端的超时与底层 Transport。
func (c *Client) observedHTTPClient(t CallType, endpoint, modelName string) *http.Client {
	base := http.DefaultTransport
	timeout := 60 * time.Second
	if c.http != nil {
		if c.http.Transport != nil {
			base = c.http.Transport
		}
		if c.http.Timeout > 0 {
			timeout = c.http.Timeout
		}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &observingTransport{
			base:     base,
			db:       c.db,
			callType: t,
			endpoint: endpoint,
			model:    modelName,
		},
	}
}

// observingTransport 在统一 HTTP 传输层注入网关会话头、采集 OmiRouter 计量响应头并写入 llm_calls。
type observingTransport struct {
	base     http.RoundTripper
	db       bun.IDB
	callType CallType
	endpoint string
	model    string
}

// RoundTrip 实现 http.RoundTripper：透传会话粘性头、记录非流式调用的请求、响应、用量、费用和耗时。
func (t *observingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	cloned := req.Clone(ctx)
	if sid := SessionFromContext(ctx); sid != "" && cloned.Header.Get("X-Omirouter-Session") == "" {
		cloned.Header.Set("X-Omirouter-Session", sid)
	}

	var reqJSON any = map[string]any{}
	if cloned.Body != nil {
		rawReq, err := io.ReadAll(io.LimitReader(cloned.Body, 4<<20))
		_ = cloned.Body.Close()
		if err != nil {
			return nil, errors.Wrap(err, "read llm request body")
		}
		cloned.Body = io.NopCloser(bytes.NewReader(rawReq))
		cloned.ContentLength = int64(len(rawReq))
		var obj map[string]any
		if json.Unmarshal(rawReq, &obj) == nil && obj != nil {
			reqJSON = obj
		}
	}

	target, _ := ctx.Value(targetCtxKey{}).(Target)
	meta := target.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	call := &Call{
		ID:         uuid.Must(uuid.NewV7()).String(),
		CreatedAt:  now,
		UpdatedAt:  now,
		CallType:   t.callType,
		Operation:  target.Operation,
		Meta:       meta,
		Endpoint:   t.endpoint,
		Model:      t.model,
		Request:    reqJSON,
		TargetType: target.Type,
		TargetID:   target.ID,
	}
	if target.Tenant != "" {
		call.TenantID = &target.Tenant
	}
	if slot, ok := ctx.Value(callIDCtxKey{}).(*string); ok && slot != nil {
		*slot = call.ID
	}

	start := time.Now()
	resp, callErr := t.base.RoundTrip(cloned)
	call.DurationMS = int(time.Since(start).Milliseconds())

	if callErr != nil {
		call.Error = callErr.Error()
		if _, err := t.db.NewInsert().Model(call).Exec(context.WithoutCancel(ctx)); err != nil {
			return nil, errors.Join(callErr, errors.Wrap(err, "record llm call"))
		}
		return nil, callErr
	}

	// 流式 SSE 响应直接透传，不缓冲整个响应体。
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type"))), "text/event-stream") {
		call.RequestID = resp.Header.Get("X-Omirouter-Request-Id")
		return resp, nil
	}

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	_ = resp.Body.Close()
	if readErr != nil {
		call.Error = readErr.Error()
		if _, err := t.db.NewInsert().Model(call).Exec(context.WithoutCancel(ctx)); err != nil {
			return nil, errors.Join(readErr, errors.Wrap(err, "record llm call"))
		}
		return nil, readErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))

	fillCallFromResponse(call, resp.StatusCode, resp.Header, data)
	if _, err := t.db.NewInsert().Model(call).Exec(context.WithoutCancel(ctx)); err != nil {
		return nil, errors.Wrap(err, "record llm call")
	}
	return resp, nil
}

// responseEnvelope 是非流式 OpenAI 兼容响应中用于补充记账的最小字段。
type responseEnvelope struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
		Cost *float64 `json:"cost"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// fillCallFromResponse 优先从 OmiRouter 统一计量响应头填充调用记录，未提供响应头时回退解析标准 JSON 响应体。
func fillCallFromResponse(call *Call, statusCode int, h http.Header, data []byte) {
	call.RequestID = h.Get("X-Omirouter-Request-Id")
	call.Response = string(data)

	hasHeaderTokens := false
	if v, ok := parseHeaderInt(h.Get("X-Omirouter-Prompt-Tokens")); ok {
		call.PromptTokens = v
		hasHeaderTokens = true
	}
	if v, ok := parseHeaderInt(h.Get("X-Omirouter-Cached-Tokens")); ok {
		call.CachedTokens = v
		hasHeaderTokens = true
	}
	if v, ok := parseHeaderInt(h.Get("X-Omirouter-Completion-Tokens")); ok {
		call.CompletionTokens = v
		hasHeaderTokens = true
	}
	if v, ok := parseHeaderInt(h.Get("X-Omirouter-Reasoning-Tokens")); ok {
		call.ReasoningTokens = v
		hasHeaderTokens = true
	}
	if rawCost := strings.TrimSpace(h.Get("X-Omirouter-Cost")); rawCost != "" {
		if cost, err := strconv.ParseFloat(rawCost, 64); err == nil {
			call.Cost = &cost
		}
	}

	var env responseEnvelope
	if json.Unmarshal(data, &env) == nil {
		if !hasHeaderTokens {
			call.PromptTokens = env.Usage.PromptTokens
			call.CachedTokens = env.Usage.PromptTokensDetails.CachedTokens
			call.CompletionTokens = env.Usage.CompletionTokens
			call.ReasoningTokens = env.Usage.CompletionTokensDetails.ReasoningTokens
		}
		if call.Cost == nil && env.Usage.Cost != nil {
			call.Cost = env.Usage.Cost
		}
		if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices && len(env.Choices) > 0 {
			call.Response = env.Choices[0].Message.Content
		}
		if statusCode >= http.StatusMultipleChoices || env.Error != nil {
			msg := ""
			if env.Error != nil {
				msg = env.Error.Message
			}
			call.Error = "model returned status " + strconv.Itoa(statusCode) + ": " + msg
		}
	} else if statusCode >= http.StatusMultipleChoices {
		call.Error = "model returned status " + strconv.Itoa(statusCode)
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
