package llm_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
)

func TestSaveSettings(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	group := llm.NewGroup(env.DB, env.Crypt)
	save := llm.NewSaveSettings(env.DB, group)

	// 没有保存过时网关和调用类型模型都为空
	s, err := group.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s != (llm.Settings{}) {
		t.Fatalf("默认值：%+v", s)
	}

	cfg := llm.Settings{
		Gateway: llm.Gateway{BaseURL: "https://gateway.example.com/v1", APIKey: "sk-gateway-secret"},
		Models: llm.Models{
			Chat:        "gpt-4o",
			FastRouter:  "gpt-4o-mini",
			Translation: "translate-fast",
			Embedding:   "text-embedding-3-small",
			Rerank:      "rerank-v3",
			Transcribe:  "whisper-1",
		},
		DefaultBudget: llm.Budget{DailyCostUSD: 25, MonthlyCostUSD: 300, DailyTotalTokens: 500000},
	}
	if err := save.Handle(ctx, apptest.Admin, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := group.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != cfg {
		t.Fatalf("读回 %+v，期望 %+v", got, cfg)
	}
	// settings 表中 api_key 必须以密文保存，不能出现明文密钥
	var raw string
	if err := env.DB.NewRaw("SELECT payload::text FROM settings WHERE name = 'llm' AND tenant = ''").Scan(ctx, &raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "sk-gateway-secret") || !strings.Contains(raw, "enc:v1:") {
		t.Fatalf("settings 表未加密保存 api_key：%s", raw)
	}

	// 只改翻译模型名：操作日志只记下 models.translation；没有修改时不记录
	cfg.Models.Translation = "translate-pro"
	for range 2 {
		if err := save.Handle(ctx, apptest.Admin, cfg); err != nil {
			t.Fatal(err)
		}
	}
	var logs []string
	if err := env.DB.NewRaw("SELECT properties::text FROM activity_log WHERE event = 'platform_settings.changed' ORDER BY created_at").Scan(ctx, &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || !strings.Contains(logs[1], `"models.translation"`) {
		t.Fatalf("操作日志：%v", logs)
	}
	// 操作日志不记录密钥的值
	if slices.ContainsFunc(logs, func(l string) bool { return strings.Contains(l, "secret") }) {
		t.Fatalf("操作日志中有密钥：%v", logs)
	}
}
