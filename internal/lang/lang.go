// Package lang 集中维护应用界面语言常量以及业务语言标签（BCP 47）的比较、匹配和规范化。
package lang

import (
	"strings"

	"golang.org/x/text/language"
)

const (
	ZhCN = "zh-CN" // 简体中文
	En   = "en"    // English
)

// UILanguages 是应用支持的界面语言，与 locales/ 中的翻译文件一一对应。
var UILanguages = []string{ZhCN, En}

var uiMatcher = language.NewMatcher([]language.Tag{
	language.Make(En),
	language.Make(ZhCN),
})

// Same 报告两个 BCP 47 标签是不是同一种语言与书写变体：
//   - 先比较主语言（base）和书写方式（script，未显式写出时按 CLDR 默认推断，如 zh-CN == zh-Hans，hi != hi-Latn）；
//   - 当双方都显式指定了不同地区且主语言不是中文时（如 pt-BR 与 pt-PT、es-MX 与 es-ES），视为不同变体；
//   - 一方未显式指定地区时（如 pt 与 pt-BR、en 与 en-US）视为兼容相同。
//
// 解析不了的标签按小写比较整个标签。
func Same(a, b string) bool {
	ta, errA := language.Parse(strings.ReplaceAll(strings.TrimSpace(a), "_", "-"))
	tb, errB := language.Parse(strings.ReplaceAll(strings.TrimSpace(b), "_", "-"))
	if errA != nil || errB != nil {
		return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	baseA, _ := ta.Base()
	baseB, _ := tb.Base()
	if baseA != baseB {
		return false
	}
	scriptA, _ := ta.Script()
	scriptB, _ := tb.Script()
	if scriptA != scriptB {
		return false
	}
	if baseA.String() != "zh" {
		regA, confA := ta.Region()
		regB, confB := tb.Region()
		if confA == language.Exact && confB == language.Exact && regA != regB {
			return false
		}
	}
	return true
}

// Canonical 返回标签 tag 的规范写法（hi-latn → hi-Latn，zh_CN → zh-CN，pt-br → pt-BR）；不是合法的 BCP 47 标签时 ok 为 false。
func Canonical(tag string) (canonical string, ok bool) {
	t, err := language.Parse(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
	if err != nil || t == language.Und {
		return "", false
	}
	return t.String(), true
}

// MatchUI 从任意 BCP 47 标签或 HTTP Accept-Language 值中匹配最接近的受支持界面语言（zh-CN 或 en）；无法匹配时返回 en。
func MatchUI(tagOrHeader string) string {
	s := strings.TrimSpace(tagOrHeader)
	if s == "" {
		return En
	}
	tags, _, err := language.ParseAcceptLanguage(strings.ReplaceAll(s, "_", "-"))
	if err != nil || len(tags) == 0 {
		t, parseErr := language.Parse(strings.ReplaceAll(s, "_", "-"))
		if parseErr != nil || t == language.Und {
			return En
		}
		tags = []language.Tag{t}
	}
	_, idx, conf := uiMatcher.Match(tags...)
	if conf == language.No {
		return En
	}
	if idx == 1 {
		return ZhCN
	}
	return En
}

// FromAcceptLanguage 从 HTTP Accept-Language 请求头中提取首个合法的规范 BCP 47 语言标签；没有时返回空字符串。
func FromAcceptLanguage(header string) string {
	s := strings.TrimSpace(header)
	if s == "" {
		return ""
	}
	tags, _, err := language.ParseAcceptLanguage(strings.ReplaceAll(s, "_", "-"))
	if err != nil {
		return ""
	}
	for _, t := range tags {
		if t == language.Und {
			continue
		}
		if c, ok := Canonical(t.String()); ok {
			return c
		}
	}
	return ""
}
