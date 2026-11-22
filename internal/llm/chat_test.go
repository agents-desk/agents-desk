package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"agents-desk/internal/apptest"
)

// TestIsOpenRouter 检查 OpenRouter 域名判断：只匹配 openrouter.ai 及其子域名。
func TestIsOpenRouter(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]bool{
		"https://openrouter.ai/api/v1":     true,
		"https://api.openrouter.ai/v1":     true,
		"https://OPENROUTER.AI/api/v1":     true,
		"https://evilopenrouter.ai/api/v1": false,
		"https://api.openai.com/v1":        false,
		"not a url":                        false,
	} {
		if got := isOpenRouter(raw); got != want {
			t.Errorf("isOpenRouter(%q) = %v，应当是 %v", raw, got, want)
		}
	}
}

// TestJSONNoReasoning 检查只有显式设置 NoReasoning 时才在 OpenRouter 请求上关掉思考。
func TestJSONNoReasoning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		bodies = append(bodies, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": `{"ok":true}`}}},
		})
	}))
	t.Cleanup(srv.Close)

	// 用自定义 Transport 把发往 openrouter.ai 的请求转到本地测试服务器
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.URL.Scheme = srvURL.Scheme
			clone.URL.Host = srvURL.Host
			return srv.Client().Transport.RoundTrip(clone)
		}),
	}
	group := NewGroup(env.DB)
	if err := group.Save(ctx, Settings{Translation: Endpoint{
		BaseURL: "https://openrouter.ai/api/v1", APIKey: "sk-test", Model: "test-model",
	}}); err != nil {
		t.Fatal(err)
	}
	client := NewClient(env.DB, group, httpClient)
	schema := map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false}

	var out struct {
		OK bool `json:"ok"`
	}
	if err := client.JSON(ctx, Translation, Target{Type: "test", ID: "1", Operation: "incoming", Meta: map[string]any{"targets": []string{"zh-CN"}}}, JSONRequest{
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
	if _, has := bodies[0]["reasoning"]; has {
		t.Fatalf("NoReasoning=false 不应当带 reasoning：%+v", bodies[0])
	}
	reasoning, _ := bodies[1]["reasoning"].(map[string]any)
	if reasoning["enabled"] != false {
		t.Fatalf("NoReasoning=true 应当带 reasoning.enabled=false：%+v", bodies[1])
	}
	var calls []Call
	if err := env.DB.NewSelect().Model(&calls).OrderExpr("target_id ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Operation != "incoming" || calls[1].Operation != "reply" {
		t.Fatalf("llm_calls operation 未正确记录：%+v", calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
