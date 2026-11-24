package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agents-desk/internal/apptest"
)

// TestJSONGatewayHeadersAndStandardParams 检查通过 tenon/ai 发送标准 OpenAI 请求（含 reasoning_effort="none"）、
// 透传 X-Omirouter-Session，并优先从 OmiRouter 统一计量响应头落库 llm_calls。
func TestJSONGatewayHeadersAndStandardParams(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	var bodies []map[string]any
	var sessionHeaders []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		bodies = append(bodies, body)
		sessionHeaders = append(sessionHeaders, r.Header.Get("X-Omirouter-Session"))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Omirouter-Request-Id", "req-gw-123")
		w.Header().Set("X-Omirouter-Cost", "0.00125")
		w.Header().Set("X-Omirouter-Prompt-Tokens", "200")
		w.Header().Set("X-Omirouter-Cached-Tokens", "80")
		w.Header().Set("X-Omirouter-Completion-Tokens", "45")
		w.Header().Set("X-Omirouter-Reasoning-Tokens", "12")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": `{"ok":true}`}}},
		})
	}))
	t.Cleanup(srv.Close)

	group := NewGroup(env.DB, env.Crypt)
	if err := group.Save(ctx, Settings{
		Gateway: Gateway{BaseURL: srv.URL + "/v1", APIKey: "sk-gw-test"},
		Models:  Models{Translation: "translate-fast"},
	}); err != nil {
		t.Fatal(err)
	}
	client := NewClient(env.DB, group, srv.Client())
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
		"required":             []string{"ok"},
		"additionalProperties": false,
	}

	var out struct {
		OK bool `json:"ok"`
	}
	if err := client.JSON(WithSession(ctx, "conv-99"), Translation, Target{
		Type: "test", ID: "1", Operation: "incoming", Meta: map[string]any{"targets": []string{"zh-CN"}},
	}, JSONRequest{
		Messages: []Message{{Role: "user", Content: "hi"}}, SchemaName: "t", Schema: schema, NoReasoning: false,
	}, &out); err != nil {
		t.Fatal(err)
	}
	if err := client.JSON(ctx, Translation, Target{Type: "test", ID: "2", Operation: "reply"}, JSONRequest{
		Messages: []Message{{Role: "user", Content: "hi"}}, SchemaName: "t", Schema: schema, NoReasoning: true,
	}, &out); err != nil {
		t.Fatal(err)
	}

	if len(bodies) != 2 {
		t.Fatalf("请求次数：%d", len(bodies))
	}
	if sessionHeaders[0] != "conv-99" || sessionHeaders[1] != "" {
		t.Fatalf("X-Omirouter-Session = %v", sessionHeaders)
	}
	if _, has := bodies[0]["reasoning_effort"]; has {
		t.Fatalf("NoReasoning=false 不应当带 reasoning_effort：%+v", bodies[0])
	}
	if bodies[1]["reasoning_effort"] != "none" {
		t.Fatalf("NoReasoning=true 应当带 reasoning_effort=\"none\"：%+v", bodies[1])
	}

	var calls []Call
	if err := env.DB.NewSelect().Model(&calls).OrderExpr("target_id ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Operation != "incoming" || calls[1].Operation != "reply" {
		t.Fatalf("llm_calls operation 未正确记录：%+v", calls)
	}
	c0 := calls[0]
	if c0.RequestID != "req-gw-123" || c0.PromptTokens != 200 || c0.CachedTokens != 80 ||
		c0.CompletionTokens != 45 || c0.ReasoningTokens != 12 || c0.Cost == nil || *c0.Cost != 0.00125 {
		t.Fatalf("网关响应头未正确落库：%+v", c0)
	}
}

// TestStreamObserveRecordsUsage 检查流式 SSE 模型调用由 ai.ObserveTransport 完整记录内容与用量。
func TestStreamObserveRecordsUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Omirouter-Request-Id", "req-stream-1")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"choices":[{"delta":{"content":"你好"}}]}`,
			`{"choices":[{"delta":{"content":"，世界"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens_details":{"reasoning_tokens":5}}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	group := NewGroup(env.DB, env.Crypt)
	if err := group.Save(ctx, Settings{
		Gateway: Gateway{BaseURL: srv.URL + "/v1", APIKey: "sk-stream-secret"},
		Models:  Models{Translation: "chat-stream"},
	}); err != nil {
		t.Fatal(err)
	}
	client := NewClient(env.DB, group, srv.Client())
	callCtx := WithTarget(WithSession(ctx, "conv-stream"), Target{Type: "conversation", ID: "c1", Operation: "draft"})
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"chat-stream","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.observedHTTPClient(Translation, srv.URL+"/v1", "chat-stream").Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rawSSE, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawSSE), "你好") || !strings.Contains(string(rawSSE), "，世界") {
		t.Fatalf("流式响应未透传：%s", string(rawSSE))
	}

	var calls []Call
	if err := env.DB.NewSelect().Model(&calls).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("期望 1 条流式 llm_calls 记录，实际 %d", len(calls))
	}
	c := calls[0]
	if c.RequestID != "req-stream-1" || c.Response != "你好，世界" ||
		c.PromptTokens != 120 || c.CachedTokens != 40 || c.CompletionTokens != 30 || c.ReasoningTokens != 5 {
		t.Fatalf("流式观测落库字段不匹配：%+v", c)
	}
}
