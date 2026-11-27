package translation

import (
	"context"
	"encoding/json"
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
// 按时间先后，每行“Customer: ……”或“Support: ……”。before 为 0 时取最近的。没有时返回空字符串。
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

// Incoming 识别工作区 tenant 中消息 it 的语言和书写/地区变体，并翻译成 targets 中的每一种语言；targets 为空时只识别。
// usual 是发言者常用的语言（还不知道时为空），和它之前的几条消息（history）一起作为参考。判断不出语言时返回
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
		"language": map[string]any{"type": "string", "description": `BCP 47 tag of the message's language, with script or region subtags when applicable; "und" when it cannot be told`},
	}
	required := []string{"language"}
	steps := []string{
		`1. Identify the language of the message and how it is written. Answer with a BCP 47 tag. Add a script subtag when written in an unusual script (for example "hi-Latn" for Hindi in Latin letters, "ar-Latn" for Arabic in Latin letters), and include the region subtag when there is a major regional variant (for example "pt-BR" for Brazilian Portuguese, "pt-PT" for European Portuguese, "zh-TW" for Traditional Chinese, "es-419" for Latin American Spanish). Examples: "hi-Latn", "hi", "en", "zh-Hans", "pt-BR", "ar".`,
		`When the message is too short or does not belong to any one language, answer "und": for example "ok", "thx", "yes", emoji, numbers, order numbers, codes or links.`,
	}
	if usual != "" {
		steps = append(steps, "This speaker usually writes in "+usual+". Use it only as a hint for script or regional variant; always answer with the actual language of this message.")
	}
	if len(targets) > 0 {
		props := map[string]any{}
		for _, target := range targets {
			props[target] = map[string]any{"type": "string", "description": "the message translated into " + target + `, or "" when it needs no translation`}
		}
		properties["translations"] = map[string]any{"type": "object", "properties": props, "required": targets, "additionalProperties": false}
		required = append(required, "translations")
		steps = append(steps, "2. Translate the message into each of these languages: "+strings.Join(targets, ", ")+
			`. Answer "" for a language when the message is already in it, or when the language is "und". Respect any script or region subtag in the target tag (such as "hi-Latn" or "pt-BR").`)
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	system := strings.Join(nonEmpty(
		"You identify the language of messages in a multilingual customer support platform, and translate them accurately.",
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
		fmt.Sprintf("You translate a customer support message written in %s into each of these languages: %s. Respect any script or region subtag in each target tag (such as \"hi-Latn\" or \"pt-BR\").", source, strings.Join(targets, ", ")),
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

// TicketMetaTranslation 是工单标题和描述的一次识别与多目标语言翻译结果。
type TicketMetaTranslation struct {
	Language     string            `json:"language"`
	Titles       map[string]string `json:"titles"`
	Descriptions map[string]string `json:"descriptions"`
}

// TranslateTicketMeta 一次调用模型识别工单标题与描述的源语言，并将其翻译成 targets 中的每一种语言。
// knownSource 非空时不重新识别语言，直接按已知源语言补译 targets。
func (tr *Translator) TranslateTicketMeta(ctx context.Context, tenant, ticketID, title, description, knownSource string, targets []string) (*TicketMetaTranslation, error) {
	if strings.TrimSpace(title) == "" && strings.TrimSpace(description) == "" {
		return &TicketMetaTranslation{Language: conversation.Undetermined, Titles: map[string]string{}, Descriptions: map[string]string{}}, nil
	}
	if knownSource != "" && len(targets) == 0 {
		return &TicketMetaTranslation{Language: knownSource, Titles: map[string]string{}, Descriptions: map[string]string{}}, nil
	}
	s, err := tr.settings.Tenant(tenant).Get(ctx)
	if err != nil {
		return nil, err
	}
	combined := strings.TrimSpace(title + "\n" + description)
	properties := map[string]any{}
	required := []string{}
	var steps []string
	if knownSource == "" {
		properties["language"] = map[string]any{
			"type":        "string",
			"description": `BCP 47 tag of the ticket title and description (with script or region subtags such as "hi-Latn" or "pt-BR"), or "und" when it cannot be told`,
		}
		required = append(required, "language")
		steps = append(steps,
			`1. Identify the language of the ticket title and description as a BCP 47 tag (preserve script or regional variants such as "hi-Latn", "pt-BR", "zh-TW"; answer "und" if it cannot be determined).`)
	}
	if len(targets) > 0 {
		titleProps := map[string]any{}
		descProps := map[string]any{}
		for _, target := range targets {
			titleProps[target] = map[string]any{"type": "string", "description": "ticket title translated into " + target + `, or "" when unchanged or not needed`}
			descProps[target] = map[string]any{"type": "string", "description": "ticket description translated into " + target + `, or "" when empty, unchanged, or not needed`}
		}
		properties["titles"] = map[string]any{"type": "object", "properties": titleProps, "required": targets, "additionalProperties": false}
		properties["descriptions"] = map[string]any{"type": "object", "properties": descProps, "required": targets, "additionalProperties": false}
		required = append(required, "titles", "descriptions")
		steps = append(steps, fmt.Sprintf("2. Translate the ticket title and description into each of these languages: %s. Respect script and regional variants (such as \"hi-Latn\" or \"pt-BR\"). Return \"\" when a field is empty or already in that language.", strings.Join(targets, ", ")))
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	system := strings.Join(nonEmpty(
		"You identify the language of a support ticket's title and description and translate them for multilingual team members and customers.",
		strings.Join(steps, "\n"),
		rules,
		glossaryHint(s.Glossary, combined, targets),
		"Reply with JSON only.",
	), "\n\n")
	userPayload, _ := json.Marshal(map[string]string{"title": title, "description": description})
	var out TicketMetaTranslation
	err = tr.llm.JSON(llm.WithSession(ctx, ticketID), llm.Translation, llm.Target{
		Tenant: tenant, Type: "ticket", ID: ticketID, Operation: "ticket_meta",
		Meta: map[string]any{"source": knownSource, "targets": targets},
	}, llm.JSONRequest{
		Messages:    []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: string(userPayload)}},
		SchemaName:  "ticket_meta_translation",
		Schema:      schema,
		NoReasoning: true,
	}, &out)
	if err != nil {
		return nil, err
	}
	source := knownSource
	if source == "" {
		if out.Language == "" || strings.EqualFold(out.Language, conversation.Undetermined) {
			return &TicketMetaTranslation{Language: conversation.Undetermined, Titles: map[string]string{}, Descriptions: map[string]string{}}, nil
		}
		source = withScript(out.Language, combined)
	}
	titles := map[string]string{}
	descs := map[string]string{}
	for _, target := range targets {
		if lang.Same(source, target) {
			continue
		}
		if v := strings.TrimSpace(out.Titles[target]); v != "" && strings.TrimSpace(title) != "" {
			titles[target] = v
		}
		if v := strings.TrimSpace(out.Descriptions[target]); v != "" && strings.TrimSpace(description) != "" {
			descs[target] = v
		}
	}
	return &TicketMetaTranslation{Language: source, Titles: titles, Descriptions: descs}, nil
}

// TranslateNoteText 识别一段独立备注文本（如联系人长期备注）的源语言并翻译成 targets 中的每一种语言。
// knownSource 非空时不重新识别语言，直接按已知源语言补译 targets。
func (tr *Translator) TranslateNoteText(ctx context.Context, tenant, targetType, targetID, text, knownSource string, targets []string) (string, map[string]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return conversation.Undetermined, map[string]string{}, nil
	}
	if knownSource != "" && len(targets) == 0 {
		return knownSource, map[string]string{}, nil
	}
	s, err := tr.settings.Tenant(tenant).Get(ctx)
	if err != nil {
		return "", nil, err
	}
	properties := map[string]any{}
	required := []string{}
	var steps []string
	if knownSource == "" {
		properties["language"] = map[string]any{
			"type":        "string",
			"description": `BCP 47 tag of the note text (preserve script/region subtags such as "hi-Latn" or "pt-BR"), or "und" when it cannot be told`,
		}
		required = append(required, "language")
		steps = append(steps, `1. Identify the language of the note as a BCP 47 tag (preserve script/region subtags such as "hi-Latn", "pt-BR", "zh-TW"; answer "und" if undetermined).`)
	}
	if len(targets) > 0 {
		props := map[string]any{}
		for _, target := range targets {
			props[target] = map[string]any{"type": "string", "description": "the note translated into " + target + `, or "" when it needs no translation`}
		}
		properties["translations"] = map[string]any{"type": "object", "properties": props, "required": targets, "additionalProperties": false}
		required = append(required, "translations")
		steps = append(steps, "2. Translate the note into each of these languages: "+strings.Join(targets, ", ")+`. Return "" when already in that language or "und".`)
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	system := strings.Join(nonEmpty(
		"You identify the language of internal customer notes and translate them for multilingual team members.",
		strings.Join(steps, "\n"),
		rules,
		glossaryHint(s.Glossary, trimmed, targets),
		"Reply with JSON only.",
	), "\n\n")
	var out incoming
	err = tr.llm.JSON(llm.WithSession(ctx, targetID), llm.Translation, llm.Target{
		Tenant: tenant, Type: targetType, ID: targetID, Operation: "note_text",
		Meta: map[string]any{"source": knownSource, "targets": targets},
	}, llm.JSONRequest{
		Messages:    []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: trimmed}},
		SchemaName:  "note_translation",
		Schema:      schema,
		NoReasoning: true,
	}, &out)
	if err != nil {
		return "", nil, err
	}
	source := knownSource
	if source == "" {
		if out.Language == "" || strings.EqualFold(out.Language, conversation.Undetermined) {
			return conversation.Undetermined, map[string]string{}, nil
		}
		source = withScript(out.Language, trimmed)
	}
	translations := map[string]string{}
	for _, target := range targets {
		if v := strings.TrimSpace(out.Translations[target]); v != "" && !lang.Same(source, target) {
			translations[target] = v
		}
	}
	return source, translations, nil
}

// replyResult 是回复翻译时模型的回答：同时判断文本本身是否已经属于目标语言。
type replyResult struct {
	Needed      *bool  `json:"needed,omitempty"`
	Translation string `json:"translation"`
}

// translated 是只要一段译文时模型的回答（如盲回译）。
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

// replyTranslationSchema 返回回复翻译的 JSON schema：包含是否需要翻译与译文。
func replyTranslationSchema(to string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"needed":      map[string]any{"type": "boolean", "description": "false if the message is already written in " + to + " or needs no translation (such as numbers, codes, or emoji); true otherwise"},
			"translation": map[string]any{"type": "string", "description": "the message in " + to + "; when needed is false, return the original text unchanged"},
		},
		"required":             []string{"needed", "translation"},
		"additionalProperties": false,
	}
}

// ReplyText 把成员在工作区 tenant 的会话 conversationID 中写的文字 text 翻译成目标语言 to（不做盲回译）：
// 按文本自身的真实语言判断，不强行假设作者只用偏好语言 from 打字；若文本本身已是 to 或无需翻译，返回 needed=false 与原文。
// 按工作区的语气（可由 toneOverride 单次覆盖）和术语表，带上会话最近的几条消息作参考。模型没有配置时返回 llm.ErrNotConfigured。
func (tr *Translator) ReplyText(ctx context.Context, tenant, conversationID, text, from, to string, toneOverride ...Tone) (string, bool, error) {
	s, err := tr.settings.Tenant(tenant).Get(ctx)
	if err != nil {
		return "", false, err
	}
	chosenTone := s.Tone
	if len(toneOverride) > 0 && toneOverride[0] != "" {
		chosenTone = toneOverride[0]
	}
	tone := toneHint[chosenTone]
	if tone == "" {
		tone = toneHint[Neutral]
	}
	earlier, err := tr.history(ctx, tenant, conversationID, 0)
	if err != nil {
		return "", false, err
	}
	replyTarget := llm.Target{
		Tenant: tenant, Type: "conversation", ID: conversationID, Operation: "reply",
		Meta: map[string]any{"from": from, "to": to},
	}

	system := strings.Join(nonEmpty(
		"You help a team member communicate with recipients in their language.",
		fmt.Sprintf(`Detect the actual language of the message and translate it into %s. Do not assume the message is written in %s. When %s has a script or regional subtag (for example "hi-Latn" or "pt-BR"), write in that script and regional style.`, to, from, to),
		fmt.Sprintf(`If the message is already written in %s, or contains only language-neutral text (order numbers, codes, URLs, emoji), set needed to false and return the original text unchanged.`, to),
		tone,
		rules,
		glossaryHint(s.Glossary, text, []string{to}),
		earlier,
		"Reply with JSON only.",
	), "\n\n")
	var out replyResult
	err = tr.llm.JSON(llm.WithSession(ctx, conversationID), llm.Translation, replyTarget, llm.JSONRequest{
		Messages:    []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: text}},
		SchemaName:  "reply_translation",
		Schema:      replyTranslationSchema(to),
		NoReasoning: true,
	}, &out)
	if err != nil {
		return "", false, err
	}
	if out.Needed != nil && !*out.Needed {
		return text, false, nil
	}
	if strings.TrimSpace(out.Translation) == "" {
		return text, false, nil
	}
	return out.Translation, true, nil
}

// Reply 把成员在工作区 tenant 的会话 conversationID 中写的文字 text 翻译成 to，并把译文盲回译为成员语言 from 供其核对。
// 若文本本身已是 to（needed=false），直接返回原文且跳过盲回译。
func (tr *Translator) Reply(ctx context.Context, tenant, conversationID, text, from, to string, toneOverride ...Tone) (translation, back string, needed bool, err error) {
	translatedText, needed, err := tr.ReplyText(ctx, tenant, conversationID, text, from, to, toneOverride...)
	if err != nil {
		return "", "", false, err
	}
	if !needed {
		return text, "", false, nil
	}
	if from == "" || lang.Same(to, from) {
		return translatedText, "", true, nil
	}

	// 盲回译：只给译文，逐字译回成员的沟通语言 from
	system := strings.Join([]string{
		fmt.Sprintf("Translate this message from %s into %s literally, so that a reader can check exactly what it says. "+
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
		Messages:    []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: translatedText}},
		SchemaName:  "back_translation",
		Schema:      translationSchema("the message translated back into " + from),
		NoReasoning: true,
	}, &backOut)
	if err != nil {
		return "", "", false, err
	}
	return translatedText, backOut.Translation, true, nil
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
