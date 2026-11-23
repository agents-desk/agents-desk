// Package lang 集中维护应用界面语言常量以及业务语言标签（BCP 47）的比较和规范化。
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

// Same 报告两个 BCP 47 标签是不是同一种语言、同一种书写方式：主语言相同，书写方式也相同。没有写书写方式时按这门语言
// 在这个地区通常的书写方式推断（CLDR），例如 zh-CN 和 zh-Hans 相同，zh-TW 是繁体，hi 是天城文、和 hi-Latn（用拉丁字母写的
// 印地语）不同。地区本身不算：en 和 en-US 相同。解析不了的标签按小写比较整个标签。
func Same(a, b string) bool {
	ta, errA := language.Parse(strings.ReplaceAll(a, "_", "-"))
	tb, errB := language.Parse(strings.ReplaceAll(b, "_", "-"))
	if errA != nil || errB != nil {
		return strings.EqualFold(a, b)
	}
	baseA, _ := ta.Base()
	baseB, _ := tb.Base()
	scriptA, _ := ta.Script()
	scriptB, _ := tb.Script()
	return baseA == baseB && scriptA == scriptB
}

// Canonical 返回标签 tag 的规范写法（hi-latn → hi-Latn，zh_CN → zh-CN）；不是合法的 BCP 47 标签时 ok 为 false。
func Canonical(tag string) (canonical string, ok bool) {
	t, err := language.Parse(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
	if err != nil || t == language.Und {
		return "", false
	}
	return t.String(), true
}
