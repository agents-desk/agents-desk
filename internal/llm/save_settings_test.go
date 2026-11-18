package llm_test

import (
	"context"
	"strings"
	"testing"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
)

func TestSaveSettingsEncryptsAPIKey(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	group := llm.NewGroup(env.DB)
	save := llm.NewSaveSettings(env.DB, group, env.Crypt)
	models := llm.Models{Translation: "translate-model", LanguageDetection: "detect-model"}
	const key = "sk-test-1234567890abcd"

	v, err := save.Handle(ctx, apptest.Admin, llm.Changes{BaseURL: "https://gw.example.com/v1", APIKey: key, Models: models})
	if err != nil {
		t.Fatal(err)
	}
	if !v.APIKeySet || v.APIKeyHint != "…abcd" {
		t.Fatalf("显示：%+v", v)
	}
	// 数据库中只有密文，能用应用密钥解开
	var payload string
	if err := env.DB.NewRaw("SELECT payload::text FROM settings WHERE name = 'llm'").Scan(ctx, &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, key) {
		t.Fatalf("明文出现在数据库中：%s", payload)
	}
	saved, err := group.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := env.Crypt.Decrypt(saved.APIKeyEncrypted); err != nil || plain != key {
		t.Fatalf("解密得到 %q，%v", plain, err)
	}

	// 密钥为空时保持原来的密钥
	models.Translation = "another-model"
	v, err = save.Handle(ctx, apptest.Admin, llm.Changes{BaseURL: "https://gw.example.com/v1", Models: models})
	if err != nil {
		t.Fatal(err)
	}
	if !v.APIKeySet || v.Models.Translation != "another-model" {
		t.Fatalf("保存模型名后：%+v", v)
	}

	// 删除密钥
	v, err = save.Handle(ctx, apptest.Admin, llm.Changes{BaseURL: "https://gw.example.com/v1", ClearAPIKey: true, Models: models})
	if err != nil {
		t.Fatal(err)
	}
	if v.APIKeySet {
		t.Fatalf("删除后仍显示已设置：%+v", v)
	}

	// 操作日志不记录密钥
	var props string
	if err := env.DB.NewRaw("SELECT string_agg(properties::text, ' ') FROM activity_log WHERE event = 'platform_settings.changed'").Scan(ctx, &props); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(props, key) || strings.Contains(props, saved.APIKeyEncrypted) {
		t.Fatalf("操作日志中有密钥：%s", props)
	}
}
