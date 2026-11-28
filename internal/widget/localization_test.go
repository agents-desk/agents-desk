package widget

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
)

// TestLocalizationNormalizeAndResolve 检查多语言配置的规范化校验、字段级回退与可选语言列表推导。
func TestLocalizationNormalizeAndResolve(t *testing.T) {
	t.Parallel()

	loc, err := normalizeLocalization(Localization{
		DefaultLanguage: "zh_cn",
		Translations: map[string]LocalizedCopy{
			"zh_CN": {Title: " 在线客服 ", Greeting: "你好", OwnerTitle: "仅商务保留"},
			"hi_latn": {
				Greeting: "Namaste",
			},
			"en": {},
		},
	}, false)
	if err != nil {
		t.Fatalf("normalizeLocalization: %v", err)
	}
	if loc.DefaultLanguage != "zh-CN" {
		t.Fatalf("规范化结果异常：%+v", loc)
	}
	if _, exists := loc.Translations["en"]; exists {
		t.Fatalf("全空翻译条目应自动省略：%+v", loc.Translations)
	}
	if loc.Translations["zh-CN"].OwnerTitle != "" {
		t.Fatalf("非商务渠道应清理商务专属字段：%+v", loc.Translations["zh-CN"])
	}

	// 字段级回退：hi-Latn 只配了 Greeting，Title 自动回退主语言 zh-CN
	resolvedLang, copy := resolveLocalizedCopy(loc, "hi-Latn")
	if resolvedLang != "hi-Latn" || copy.Greeting != "Namaste" || copy.Title != "在线客服" {
		t.Fatalf("字段级回退异常：%q %+v", resolvedLang, copy)
	}
	// 未命中的语种回退主语言文案
	resolvedLang, copy = resolveLocalizedCopy(loc, "hi")
	if resolvedLang != "hi" || copy.Greeting != "你好" || copy.Title != "在线客服" {
		t.Fatalf("未命中语种回退异常：%q %+v", resolvedLang, copy)
	}

	// 非法语言标签返回 ErrInvalidLanguage
	if _, err := normalizeLocalization(Localization{DefaultLanguage: "not a tag"}, false); !errors.Is(err, ErrInvalidLanguage) {
		t.Errorf("非法 default_language：%v", err)
	}
	if _, err := normalizeLocalization(Localization{Translations: map[string]LocalizedCopy{"123": {Title: "x"}}}, false); !errors.Is(err, ErrInvalidLanguage) {
		t.Errorf("非法 translations key：%v", err)
	}
}

// TestUpdateLanguageColdStartOnly 检查访客切换语言仅在尚未发出任何消息时更新 contacts.language，
// 一旦联系人发送过消息则保持真实消息语言，不被界面切换覆盖。
func TestUpdateLanguageColdStartOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "lang")
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	registry := channel.Registry{channel.Widget: NewConnector(hub)}
	down := conversation.Downstream{Hub: hub, Roles: env.Roles, ContactPush: Push(hub)}
	send := NewSendMessage(env.DB, conversation.NewReceive(env.DB, registry, down))
	updateLang := NewUpdateLanguage(env.DB)

	_, lead := f.session(t, SessionBody{Language: "en"})
	summary, err := updateLang.Handle(ctx, lead, ContactLanguageBody{Language: "hi_latn"})
	if err != nil || summary.Language != "hi-Latn" {
		t.Fatalf("冷启动切换语言应生效：%v %+v", err, summary)
	}
	if _, err := updateLang.Handle(ctx, lead, ContactLanguageBody{Language: "bad!!"}); !errors.Is(err, ErrInvalidLanguage) {
		t.Fatalf("非法语言标签应报错：%v", err)
	}

	// 联系人发出第一条真实消息后，再切换 UI 语言不应覆盖 contacts.language
	if _, err := send.Handle(f.in(t, f.widget.ID), lead, MessageBody{Text: "mera order kahan hai"}); err != nil {
		t.Fatal(err)
	}
	after, err := updateLang.Handle(ctx, lead, ContactLanguageBody{Language: "zh-CN"})
	if err != nil || after.Language != "hi-Latn" {
		t.Fatalf("已有真实消息后不应覆盖语言先验：%v %+v", err, after)
	}
}

// TestTranslateCopy 检查 AI 翻译渠道文案的权限校验、字段展平还原以及未配置模型时的错误暴露。
func TestTranslateCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	noPerm := identity.Subject{Type: member.Type, ID: "m1", Tenant: "t1", Permissions: []string{string(auth.ConversationsRead)}}
	bizUser := identity.Subject{Type: member.Type, ID: "m2", Tenant: "t1", Permissions: []string{string(auth.BusinessChannelsUse)}}
	wildcardUser := identity.Subject{Type: member.Type, ID: "m3", Tenant: "t1", Permissions: []string{"*"}}

	var gotSource string
	var gotTargets []string
	var gotFields map[string]string
	stub := func(_ context.Context, _, _, _, source string, targets []string, fields map[string]string) (map[string]map[string]string, error) {
		gotSource = source
		gotTargets = targets
		gotFields = fields
		return map[string]map[string]string{
			"en": {
				"title":     "Global Support",
				"greeting":  "Hello!",
				"starter_0": "Pricing inquiry",
				"starter_1": "API support",
			},
			"hi-Latn": {
				"title":     "Global Support",
				"greeting":  "Namaste!",
				"starter_0": "Pricing ki jaankari",
				"starter_1": "API support",
			},
		}, nil
	}

	action := NewTranslateCopy(stub)
	if _, err := action.Handle(ctx, noPerm, TranslateCopyBody{
		SourceLanguage:  "zh-CN",
		TargetLanguages: []string{"en"},
		Copy:            LocalizedCopy{Title: "全球客服"},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("无权限应返回 ErrForbidden：%v", err)
	}

	out, err := action.Handle(ctx, bizUser, TranslateCopyBody{
		SourceLanguage:  "zh_cn",
		TargetLanguages: []string{"zh-CN", "en", "hi_latn"},
		Copy: LocalizedCopy{
			Title:    "全球客服",
			Greeting: "你好！",
			Starters: []string{"价格咨询", "API 支持"},
		},
	})
	if err != nil {
		t.Fatalf("TranslateCopy: %v", err)
	}
	if gotSource != "zh-CN" || !slices.Equal(gotTargets, []string{"en", "hi-Latn"}) || len(gotFields) != 4 {
		t.Fatalf("翻译器入参异常：source=%q targets=%v fields=%v", gotSource, gotTargets, gotFields)
	}
	if out.Translations["en"].Greeting != "Hello!" || len(out.Translations["hi-Latn"].Starters) != 2 {
		t.Fatalf("翻译还原结果异常：%+v", out.Translations)
	}

	// 未注入翻译器时返回 llm.ErrNotConfigured（同时验证通配权限 "*" 通过校验）
	if _, err := NewTranslateCopy(nil).Handle(ctx, wildcardUser, TranslateCopyBody{
		SourceLanguage:  "zh-CN",
		TargetLanguages: []string{"en"},
		Copy:            LocalizedCopy{Greeting: "你好"},
	}); !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("未配置翻译器应返回 llm.ErrNotConfigured：%v", err)
	}
}
