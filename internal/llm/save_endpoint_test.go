package llm_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
)

func TestSaveEndpointKeepsOtherPurposes(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	group := llm.NewGroup(env.DB)
	save := llm.NewSaveEndpoint(env.DB, group)

	// 没有保存过时每种用途都没有配置
	s, err := group.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s != (llm.Settings{}) {
		t.Fatalf("默认值：%+v", s)
	}

	// 两种用途各自保存，使用不同的接口；保存一种用途不改变另一种
	translate := llm.Endpoint{BaseURL: "https://a.example.com/v1", APIKey: "sk-translate-secret", Model: "translate-model"}
	detect := llm.Endpoint{BaseURL: "https://b.example.com/v1", APIKey: "sk-detect-secret", Model: "detect-model"}
	if err := save.Handle(ctx, apptest.Admin, llm.Translation, translate); err != nil {
		t.Fatal(err)
	}
	if err := save.Handle(ctx, apptest.Admin, llm.LanguageDetection, detect); err != nil {
		t.Fatal(err)
	}
	got, err := group.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := (llm.Settings{Translation: translate, LanguageDetection: detect}); got != want {
		t.Fatalf("读回 %+v，期望 %+v", got, want)
	}

	// 只改翻译的模型名：操作日志只记下这一项；没有修改时不记录
	translate.Model = "translate-model-2"
	for range 2 {
		if err := save.Handle(ctx, apptest.Admin, llm.Translation, translate); err != nil {
			t.Fatal(err)
		}
	}
	var logs []string
	if err := env.DB.NewRaw("SELECT properties::text FROM activity_log WHERE event = 'platform_settings.changed' ORDER BY created_at").Scan(ctx, &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 || !strings.Contains(logs[2], `"translation"`) || !strings.Contains(logs[2], `["model"]`) {
		t.Fatalf("操作日志：%v", logs)
	}
	// 操作日志不记录密钥的值
	if slices.ContainsFunc(logs, func(l string) bool { return strings.Contains(l, "secret") }) {
		t.Fatalf("操作日志中有密钥：%v", logs)
	}
}
