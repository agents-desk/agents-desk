package translation

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
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

// rules 是各种翻译都遵守的规则。
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

// 会话上下文：识别和翻译时带上会话中最近的几条消息作参考，短句、指代和省略据此消歧。
const (
	historyMessages = 6    // 最多带几条
	historyChars    = 1500 // 总长上限（字符），从最近的往前取
	historyEach     = 300  // 每条的长度上限（字符），超出的截断
)

// history 返回会话 conversationID 中序号小于 before 的最近几条消息（对所有人可见、有文字的消息），写成提示中的一段参考：
// 按时间先后，每行“Customer: ……”或“Support: ……”。客户的消息是原文，客服的消息是发给客户的文字（翻译后发出的是译文），
// 所以基本都是客户的语言。before 为 0 时取最近的。没有时返回空字符串。
func (tr *Translator) history(ctx context.Context, tenant, conversationID string, before int64) (string, error) {
	q := tr.db.NewSelect().Model((*conversation.Item)(nil)).Column("actor_type", "content").
		Where("tenant_id = ? AND conversation_id = ? AND kind = ? AND visibility = ? AND actor_type <> ?",
			tenant, conversationID, conversation.Message, conversation.Everyone, conversation.SystemActor).
		Where("content->>'text' <> ''").OrderExpr("seq DESC").Limit(historyMessages)
	if before > 0 {
		q = q.Where("seq < ?", before)
	}
	var items []conversation.Item
	if err := q.Scan(ctx, &items); err != nil {
		return "", errors.Wrap(err, "load conversation history")
	}
	var lines []string // 从最近的往前
	total := 0
	for _, it := range items {
		text := strings.Join(strings.Fields(it.Content.Text), " ")
		if r := []rune(text); len(r) > historyEach {
			text = string(r[:historyEach]) + "…"
		}
		who := "Support"
		if it.ActorType == conversation.ContactActor {
			who = "Customer"
		}
		line := who + ": " + text
		if total+len([]rune(line)) > historyChars {
			break
		}
		total += len([]rune(line))
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "", nil
	}
	slices.Reverse(lines)
	return "Earlier messages in this conversation, for reference only. Use them to understand short messages, references and terms; " +
		"never translate or answer them:\n<history>\n" + strings.Join(lines, "\n") + "\n</history>", nil
}

// incoming 是识别和翻译收到的消息时模型的回答。
type incoming struct {
	Language     string            `json:"language"`
	Translations map[string]string `json:"translations"`
}

// Incoming 识别工作区 tenant 中消息 it 的语言和书写方式，并翻译成 targets 中的每一种语言；targets 为空时只识别。
// usual 是这位客户目前的语言（还不知道时为空），和它之前的几条消息（history）一起作为参考。判断不出语言时返回
// conversation.Undetermined，不翻译。返回识别出的语言（BCP 47），以及和原文不同语言的那些译文。模型没有配置时返回 llm.ErrNotConfigured。
func (tr *Translator) Incoming(ctx context.Context, tenant string, it *conversation.Item, usual string, targets []string) (string, map[string]string, error) {
	s, err := tr.settings.Tenant(tenant).Get(ctx)
	if err != nil {
		return "", nil, err
	}
	text := it.SourceText()
	earlier, err := tr.history(ctx, tenant, it.ConversationID, it.Seq)
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
		earlier,
		glossaryHint(s.Glossary, text, targets),
		"Reply with JSON only.",
	), "\n\n")
	var out incoming
	err = tr.llm.JSON(llm.WithSession(ctx, it.ConversationID), llm.Translation, llm.Target{
		Tenant: tenant, Type: "timeline_item", ID: it.ID, Operation: "incoming",
		Meta: map[string]any{"targets": targets},
	}, llm.JSONRequest{
		Messages:    []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: text}},
		SchemaName:  "incoming_translation",
		Schema:      schema,
		NoReasoning: true,
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

// fillResult 是补译已知语言的消息时模型的回答。
type fillResult struct {
	Translations map[string]string `json:"translations"`
}

// Translate 把工作区 tenant 中已知语言为 source 的消息 it 翻译成 targets 中的每一种语言，不重新识别语言，带上它之前的几条消息作参考。
// 用于已识别过语言的消息的按需补译：识别一次就够，重新识别可能把短句判断成 und 而缺了译文。模型没有配置时返回 llm.ErrNotConfigured。
func (tr *Translator) Translate(ctx context.Context, tenant string, it *conversation.Item, source string, targets []string) (map[string]string, error) {
	if len(targets) == 0 {
		return map[string]string{}, nil
	}
	s, err := tr.settings.Tenant(tenant).Get(ctx)
	if err != nil {
		return nil, err
	}
	text := it.SourceText()
	earlier, err := tr.history(ctx, tenant, it.ConversationID, it.Seq)
	if err != nil {
		return nil, err
	}
	props := map[string]any{}
	for _, target := range targets {
		props[target] = map[string]any{"type": "string", "description": "the message translated into " + target}
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"translations": map[string]any{"type": "object", "properties": props, "required": targets, "additionalProperties": false},
		},
		"required":             []string{"translations"},
		"additionalProperties": false,
	}
	system := strings.Join(nonEmpty(
		fmt.Sprintf("You translate a customer support message written in %s into each of these languages: %s.", source, strings.Join(targets, ", ")),
		rules,
		earlier,
		glossaryHint(s.Glossary, text, targets),
		"Reply with JSON only.",
	), "\n\n")
	var out fillResult
	err = tr.llm.JSON(llm.WithSession(ctx, it.ConversationID), llm.Translation, llm.Target{
		Tenant: tenant, Type: "timeline_item", ID: it.ID, Operation: "fill",
		Meta: map[string]any{"source": source, "targets": targets},
	}, llm.JSONRequest{
		Messages:    []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: text}},
		SchemaName:  "fill_translation",
		Schema:      schema,
		NoReasoning: true,
	}, &out)
	if err != nil {
		return nil, err
	}
	translations := map[string]string{}
	for _, target := range targets {
		if t := out.Translations[target]; t != "" && !lang.Same(source, target) {
			translations[target] = t
		}
	}
	return translations, nil
}

// translated 是只要一段译文时模型的回答：回复的译文、回译。
type translated struct {
	Translation string `json:"translation"`
}

// translationSchema 返回只要一段译文的 JSON schema，description 说明译文是什么。
func translationSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"translation": map[string]any{"type": "string", "description": description}},
		"required":             []string{"translation"},
		"additionalProperties": false,
	}
}

// Reply 把成员在工作区 tenant 的会话 conversationID 中写的文字 text 从 from 翻译成 to，并给出回译，成员据此确认意思没有走样。
// 分两次调用模型：第一次翻译，按工作区的语气和术语表，带上会话最近的几条消息作参考；第二次是盲回译，模型只看到译文，
// 把它逐字译回 from，回译因此反映译文本身的意思。模型没有配置时返回 llm.ErrNotConfigured。
func (tr *Translator) Reply(ctx context.Context, tenant, conversationID, text, from, to string) (translation, back string, err error) {
	s, err := tr.settings.Tenant(tenant).Get(ctx)
	if err != nil {
		return "", "", err
	}
	tone := toneHint[s.Tone]
	if tone == "" {
		tone = toneHint[Neutral]
	}
	earlier, err := tr.history(ctx, tenant, conversationID, 0)
	if err != nil {
		return "", "", err
	}
	replyTarget := llm.Target{
		Tenant: tenant, Type: "conversation", ID: conversationID, Operation: "reply",
		Meta: map[string]any{"from": from, "to": to},
	}

	// 翻译
	system := strings.Join(nonEmpty(
		"You help a customer support agent reply to a customer in the customer's language.",
		fmt.Sprintf(`Translate the agent's message from %s into %s. When %s has a script subtag (for example "hi-Latn"), write in that script.`, from, to, to),
		tone,
		rules,
		glossaryHint(s.Glossary, text, []string{to}),
		earlier,
		"Reply with JSON only.",
	), "\n\n")
	var out translated
	err = tr.llm.JSON(llm.WithSession(ctx, conversationID), llm.Translation, replyTarget, llm.JSONRequest{
		Messages:    []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: text}},
		SchemaName:  "reply_translation",
		Schema:      translationSchema("the agent's message in " + to),
		NoReasoning: true,
	}, &out)
	if err != nil {
		return "", "", err
	}

	// 盲回译：只给译文
	system = strings.Join([]string{
		fmt.Sprintf("Translate this customer support message from %s into %s literally, so that a reader can check exactly what it says. "+
			"Keep its meaning, tone and details as they are; do not improve, correct or explain it.", to, from),
		"Keep order numbers, amounts, prices, phone numbers, URLs, email addresses, codes and emoji exactly as written.",
		"Reply with JSON only.",
	}, "\n\n")
	backTarget := llm.Target{
		Tenant: tenant, Type: "conversation", ID: conversationID, Operation: "back_translate",
		Meta: map[string]any{"from": to, "to": from},
	}
	var backOut translated
	err = tr.llm.JSON(llm.WithSession(ctx, conversationID), llm.Translation, backTarget, llm.JSONRequest{
		Messages:    []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: out.Translation}},
		SchemaName:  "back_translation",
		Schema:      translationSchema("the message translated back into " + from),
		NoReasoning: true,
	}, &backOut)
	if err != nil {
		return "", "", err
	}
	return out.Translation, backOut.Translation, nil
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
