// Package translation 是会话中的翻译：收到的消息在后台识别语言和书写方式，翻译成工作区成员用到的每一种语言，存为消息的
// 派生内容（Job）；成员回复前，给出发给对方的译文和回译（Preview）。翻译按工作区的语气和术语表进行（Settings）。
// 模型接口是平台设置中的“翻译”调用类型，每次调用都记在 llm_calls 中。
package translation

import (
	"context"
	"unicode"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"
	"golang.org/x/text/language"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/settings"
)

// Tone 是译文的语气。
type Tone string

const (
	Friendly Tone = "friendly" // 亲切
	Neutral  Tone = "neutral"  // 中性
	Formal   Tone = "formal"   // 正式
)

// Tones 是全部语气，显示文字是翻译文件中 translation.tone.<语气> 的消息。
var Tones = enum.New("translation.tone", Friendly, Neutral, Formal)

// Schema 让 OpenAPI 中的 Tone 只接受上面的取值，并带有显示文字。
func (Tone) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Tones) }

// Term 是术语表中的一条：一个词的固定译法，或者保持原样（例如品牌名）。
type Term struct {
	Term         string            `json:"term" minLength:"1" maxLength:"100" doc:"原文中的词，不区分大小写"`
	Keep         bool              `json:"keep" doc:"保持原样，不翻译"`
	Translations map[string]string `json:"translations,omitempty" doc:"各语言的固定译法，键是语言（BCP 47），例如 zh-CN；保持原样时不用填"`
}

// Settings 是工作区的翻译设置：语气和术语表。字段不带 omitempty，见 settings 包的说明。
type Settings struct {
	Tone     Tone   `json:"tone" doc:"回复译文的语气"`
	Glossary []Term `json:"glossary" nullable:"false" doc:"术语表"`
}

// NewGroup 返回翻译设置，每个工作区一行（Tenant），名称 translation 保存在 settings 表中，发布后保持固定。默认语气中性、没有术语。
func NewGroup(db bun.IDB) *settings.Group[Settings] {
	return settings.New(db, "translation", Settings{Tone: Neutral, Glossary: []Term{}})
}

// memberLanguages 返回工作区 tenant 中没有停用的成员用到的语言，按字母排列：收到的消息翻译成这些语言。
func memberLanguages(ctx context.Context, db bun.IDB, tenant string) ([]string, error) {
	var langs []string
	err := db.NewRaw(`SELECT DISTINCT m.language FROM members m
		JOIN authz_assignments a ON a.subject_type = 'member' AND a.subject_id = m.id::text AND a.tenant = ?
		WHERE m.disabled_at IS NULL ORDER BY m.language`, tenant).Scan(ctx, &langs)
	return langs, errors.Wrap(err, "load member languages")
}

// withScript 按原文 text 实际用的文字修正模型识别出的语言标签 tag：原文主要是拉丁字母、而这门语言通常不用拉丁字母写时
// （例如印地语），补上 -Latn，例如 hi 改为 hi-Latn。模型有时会漏掉书写方式，而回复要用客户用的文字来写。
// 标签已经写了书写方式、解析不了，或者原文主要不是拉丁字母时原样返回。
func withScript(tag, text string) string {
	t, err := language.Parse(tag)
	if err != nil {
		return tag
	}
	if _, conf := t.Script(); conf == language.Exact {
		return tag
	}
	// 汉字、假名、谚文一个字大致相当于一个词，按三个字母计
	latin, other := 0, 0
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Latin, r):
			latin++
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			other += 3
		case unicode.IsLetter(r):
			other++
		}
	}
	if latin <= other {
		return tag
	}
	base, _ := t.Base()
	if usual, _ := language.Make(base.String()).Script(); usual.String() == "Latn" {
		return tag
	}
	return base.String() + "-Latn"
}
