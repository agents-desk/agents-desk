package translation

import (
	"context"
	"fmt"
	"strings"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/conversation"
	"agents-desk/internal/lang"
	"agents-desk/internal/llm"
)

// Translator 调用模型识别语言和翻译，按工作区的语气和术语表写提示。
type Translator struct {
	db       bun.IDB
	llm      *llm.Client
	settings *settings.Group[Settings]
}

// NewTranslator 返回翻译器；client 调用平台设置中“翻译”调用类型的模型接口，s 是工作区的翻译设置。
func NewTranslator(db bun.IDB, client *llm.Client, s *settings.Group[Settings]) *Translator {
	return &Translator{db: db, llm: client, settings: s}
}

// Enabled 返回翻译是否可用：平台设置中配置了“翻译”调用类型的模型接口。
func (tr *Translator) Enabled(ctx context.Context) (bool, error) {
	return tr.llm.Configured(ctx, llm.Translation)
}

// rules 是两种翻译都遵守的规则。
const rules = `Rules:
- Translate the meaning faithfully and naturally. Do not add, omit or explain anything, and never answer the message yourself.
- Keep order numbers, amounts, prices, phone numbers, URLs, email addresses, codes and emoji exactly as written.`

// toneHint 是语气对应的提示。
var toneHint = map[Tone]string{
	Friendly: "Use a warm, friendly and polite tone, as a helpful support agent would.",
	Neutral:  "Use a neutral, polite tone.",
	Formal:   "Use a formal, respectful tone.",
}

// glossaryHint 返回术语表中出现在 text 里的词的提示：保持原样的，以及目标语言 targets 的固定译法。没有时返回空字符串。
func glossaryHint(glossary []Term, text string, targets []string) string {
	lower := strings.ToLower(text)
	var lines []string
	for _, t := range glossary {
		if t.Term == "" || !strings.Contains(lower, strings.ToLower(t.Term)) {
			continue
		}
		if t.Keep {
			lines = append(lines, fmt.Sprintf("- %q: keep it exactly as written, do not translate it", t.Term))
			continue
		}
		for _, target := range targets {
			for k, v := range t.Translations {
				if v != "" && lang.Same(k, target) {
					lines = append(lines, fmt.Sprintf("- %q: in %s always translate it as %q", t.Term, target, v))
				}
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Glossary (always follow it):\n" + strings.Join(lines, "\n")
}

// incoming 是识别和翻译收到的消息时模型的回答。
type incoming struct {
	Language     string            `json:"language"`
	Translations map[string]string `json:"translations"`
}

// Incoming 识别工作区 tenant 中消息 itemID 的文字 text 的语言和书写方式，并翻译成 targets 中的每一种语言；targets 为空时只识别。
// usual 是这位客户目前的语言（还不知道时为空），提示中作为参考。判断不出语言时返回 conversation.Undetermined，不翻译。
// 返回识别出的语言（BCP 47），以及和原文不同语言的那些译文。模型没有配置时返回 llm.ErrNotConfigured。
func (tr *Translator) Incoming(ctx context.Context, tenant, itemID, text, usual string, targets []string) (string, map[string]string, error) {
	s, err := tr.settings.Tenant(tenant).Get(ctx)
	if err != nil {
		return "", nil, err
	}
	properties := map[string]any{
		"language": map[string]any{"type": "string", "description": `BCP 47 tag of the message's language, with a script subtag when it is written in an unusual script; "und" when it cannot be told`},
	}
	required := []string{"language"}
	steps := []string{
		`1. Identify the language of the message and how it is written. Answer with a BCP 47 tag, and add a script subtag when the language is written in a script other than its usual one: for example "hi-Latn" for Hindi written in Latin letters (Hinglish), "hi" for Hindi in Devanagari, "en" for English, "zh-Hans" for Simplified Chinese, "ar" for Arabic.`,
		`When the message is too short or does not belong to any one language, answer "und": for example "ok", "thx", "yes", emoji, numbers, order numbers, codes or links.`,
	}
	if usual != "" {
		steps = append(steps, "This customer has been writing in "+usual+". Use it only to help with the script; answer with the language of this message.")
	}
	if len(targets) > 0 {
		props := map[string]any{}
		for _, target := range targets {
			props[target] = map[string]any{"type": "string", "description": "the message translated into " + target + `, or "" when it needs no translation`}
		}
		properties["translations"] = map[string]any{"type": "object", "properties": props, "required": targets, "additionalProperties": false}
		required = append(required, "translations")
		steps = append(steps, "2. Translate the message into each of these languages: "+strings.Join(targets, ", ")+
			`. Answer "" for a language when the message is already in it, or when the language is "und".`)
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	system := strings.Join(nonEmpty(
		"You identify the language of messages that customers send to a customer support team, and translate them for the team.",
		strings.Join(steps, "\n"),
		rules,
		glossaryHint(s.Glossary, text, targets),
		"Reply with JSON only.",
	), "\n\n")
	var out incoming
	err = tr.llm.JSON(ctx, llm.Translation, llm.Target{Tenant: tenant, Type: "timeline_item", ID: itemID}, llm.JSONRequest{
		Messages:   []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: text}},
		SchemaName: "incoming_translation",
		Schema:     schema,
	}, &out)
	if err != nil {
		return "", nil, err
	}
	if out.Language == "" || strings.EqualFold(out.Language, conversation.Undetermined) {
		return conversation.Undetermined, map[string]string{}, nil
	}
	// 书写方式按原文实际用的文字修正：模型有时会漏掉 -Latn
	out.Language = withScript(out.Language, text)
	translations := map[string]string{}
	for _, target := range targets {
		if t := out.Translations[target]; t != "" && !lang.Same(out.Language, target) {
			translations[target] = t
		}
	}
	return out.Language, translations, nil
}

// reply 是翻译成员的回复时模型的回答。
type reply struct {
	Translation     string `json:"translation"`
	BackTranslation string `json:"back_translation"`
}

// Reply 把成员在工作区 tenant 的会话 conversationID 中写的文字 text 从 from 翻译成 to，并把译文译回 from，
// 成员据此确认意思没有走样。按工作区的语气和术语表翻译。模型没有配置时返回 llm.ErrNotConfigured。
func (tr *Translator) Reply(ctx context.Context, tenant, conversationID, text, from, to string) (translation, back string, err error) {
	s, err := tr.settings.Tenant(tenant).Get(ctx)
	if err != nil {
		return "", "", err
	}
	tone := toneHint[s.Tone]
	if tone == "" {
		tone = toneHint[Neutral]
	}
	system := strings.Join(nonEmpty(
		"You help a customer support agent reply to a customer in the customer's language.",
		fmt.Sprintf(`1. Translate the agent's message from %s into %s. When %s has a script subtag (for example "hi-Latn"), write in that script.`, from, to, to),
		fmt.Sprintf("2. Translate your translation back into %s, literally, so the agent can check that the meaning is unchanged.", from),
		tone,
		rules,
		glossaryHint(s.Glossary, text, []string{to}),
		"Reply with JSON only.",
	), "\n\n")
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"translation":      map[string]any{"type": "string", "description": "the agent's message in " + to},
			"back_translation": map[string]any{"type": "string", "description": "the translation translated back into " + from},
		},
		"required":             []string{"translation", "back_translation"},
		"additionalProperties": false,
	}
	var out reply
	err = tr.llm.JSON(ctx, llm.Translation, llm.Target{Tenant: tenant, Type: "conversation", ID: conversationID}, llm.JSONRequest{
		Messages:   []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: text}},
		SchemaName: "reply_translation",
		Schema:     schema,
	}, &out)
	if err != nil {
		return "", "", err
	}
	return out.Translation, out.BackTranslation, nil
}

// nonEmpty 返回 parts 中不为空的那些。
func nonEmpty(parts ...string) []string {
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
