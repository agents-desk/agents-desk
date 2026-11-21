package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/settings"
)

var (
	// ErrNotConfigured 是这种调用类型还没有配置模型接口：平台管理员在管理员端的平台设置中填写。
	ErrNotConfigured = errorx.Define(errorx.Conflict, "llm.not_configured")
	// ErrFailed 是模型接口调用失败，或者回答不是要求的格式。
	ErrFailed = errorx.Define(errorx.Unavailable, "llm.failed")
)

// Message 是对话中的一条消息。
type Message struct {
	Role    string `json:"role"` // system、user
	Content string `json:"content"`
}

// JSONRequest 是要求按 JSON schema 回答的一次调用。
type JSONRequest struct {
	Messages   []Message
	SchemaName string         // schema 的名字，例如 translation
	Schema     map[string]any // 回答的 JSON schema
}

// Target 是一次调用为谁做的，写进调用记录：工作区，以及对象的类型和 ID（例如 timeline_item 和它的 ID）。
type Target struct {
	Tenant string
	Type   string
	ID     string
}

// Call 是一次调用记录，对应 llm_calls 表。
type Call struct {
	bun.BaseModel `bun:"table:llm_calls"`

	ID               string    `bun:"id,pk"`
	CreatedAt        time.Time `bun:"created_at,notnull"`
	TenantID         *string   `bun:"tenant_id"`
	CallType         CallType  `bun:"call_type,notnull"`
	Endpoint         string    `bun:"endpoint,notnull"` // 接口的地址，不含密钥
	Model            string    `bun:"model,notnull"`
	Request          any       `bun:"request,type:jsonb,notnull"`
	Response         string    `bun:"response,notnull"`
	PromptTokens     int       `bun:"prompt_tokens,notnull"`
	CompletionTokens int       `bun:"completion_tokens,notnull"`
	Cost             *float64  `bun:"cost"` // 供应商返回的费用（美元），没有时为空
	DurationMS       int       `bun:"duration_ms,notnull"`
	Error            string    `bun:"error,notnull"`
	TargetType       string    `bun:"target_type,notnull"`
	TargetID         string    `bun:"target_id,notnull"`
}

// Client 调用平台设置中配置的模型接口，并记录每次调用。可以并发使用。
type Client struct {
	db       bun.IDB
	settings *settings.Group[Settings]
	http     *http.Client
}

// NewClient 返回模型接口的客户端；settings 是平台的模型接口设置，http 发出请求（入口给出带超时的客户端）。
// 接口的地址由平台管理员填写，可以是内网自建的模型服务，所以不限制为公网地址。
func NewClient(db bun.IDB, s *settings.Group[Settings], h *http.Client) *Client {
	return &Client{db: db, settings: s, http: h}
}

// completion 是 /chat/completions 的回答中用到的部分。
type completion struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int      `json:"prompt_tokens"`
		CompletionTokens int      `json:"completion_tokens"`
		Cost             *float64 `json:"cost"` // OpenRouter 在请求带 usage.include 时返回
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// JSON 用调用类型 t 的模型接口发出 req，把按 req.Schema 给出的回答解码到 out。接口是 OpenAI 兼容的 /chat/completions，
// 用 response_format 要求严格按 schema 回答；接口在 OpenRouter 上时请求带上 usage.include，回答中有费用。
// 不论成败都在 llm_calls 中记一行。没有配置时返回 ErrNotConfigured，调用失败或回答不是要求的格式时返回 ErrFailed。
func (c *Client) JSON(ctx context.Context, t CallType, target Target, req JSONRequest, out any) error {
	s, err := c.settings.Get(ctx)
	if err != nil {
		return err
	}
	ep := *s.endpoint(t)
	if ep.BaseURL == "" || ep.Model == "" {
		return ErrNotConfigured.New()
	}
	body := map[string]any{
		"model":       ep.Model,
		"messages":    req.Messages,
		"temperature": 0.2,
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": req.SchemaName, "strict": true, "schema": req.Schema},
		},
	}
	if u, err := url.Parse(ep.BaseURL); err == nil && strings.HasSuffix(u.Hostname(), "openrouter.ai") {
		body["usage"] = map[string]any{"include": true}
	}
	call := &Call{
		ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		CallType: t, Endpoint: ep.BaseURL, Model: ep.Model, Request: body, TargetType: target.Type, TargetID: target.ID,
	}
	if target.Tenant != "" {
		call.TenantID = &target.Tenant
	}
	start := time.Now()
	callErr := c.do(ctx, ep, body, call, out)
	call.DurationMS = int(time.Since(start).Milliseconds())
	if callErr != nil {
		call.Error = callErr.Error()
	}
	// 记录用不受调用取消影响的 context：调用超时了也要留下记录
	if _, err := c.db.NewInsert().Model(call).Exec(context.WithoutCancel(ctx)); err != nil {
		return errors.Join(callErr, errors.Wrap(err, "record llm call"))
	}
	if callErr != nil {
		return ErrFailed.Wrap(callErr)
	}
	return nil
}

// do 发出请求 body，把回答、用量和费用记到 call 上，并把回答的内容解码到 out。
func (c *Client) do(ctx context.Context, ep Endpoint, body map[string]any, call *Call, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return errors.Wrap(err, "encode request")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ep.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return errors.Wrap(err, "build request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+ep.APIKey)
	res, err := c.http.Do(httpReq)
	if err != nil {
		return errors.Wrap(err, "call model")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return errors.Wrap(err, "read response")
	}
	call.Response = string(data)
	var resp completion
	if err := json.Unmarshal(data, &resp); err != nil {
		return errors.Wrapf(err, "decode response (status %d)", res.StatusCode)
	}
	call.PromptTokens, call.CompletionTokens, call.Cost = resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.Cost
	if res.StatusCode != http.StatusOK || resp.Error != nil {
		msg := ""
		if resp.Error != nil {
			msg = resp.Error.Message
		}
		return errors.Newf("model returned status %d: %s", res.StatusCode, msg)
	}
	if len(resp.Choices) == 0 {
		return errors.New("model returned no choices")
	}
	content := resp.Choices[0].Message.Content
	call.Response = content
	if err := json.Unmarshal([]byte(stripFence(content)), out); err != nil {
		return errors.Wrap(err, "decode answer")
	}
	return nil
}

// stripFence 去掉有的模型给 JSON 回答包上的 ```json 代码块标记。
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	return strings.TrimSpace(strings.TrimSuffix(s, "```"))
}
