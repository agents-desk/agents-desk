package lang

import "testing"

// TestSame 检查语言的比较：主语言、书写方式一致且不冲突显式地区变体才算同一种语言。
func TestSame(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"zh-CN", "zh-Hans", true},
		{"zh-CN", "zh-SG", true},
		{"zh-CN", "zh", true},
		{"zh-TW", "zh-Hans", false},
		{"zh-Hant", "zh-HK", true},
		{"en", "en-US", true},
		{"hi", "hi-Latn", false},
		{"hi-Latn", "hi-Latn", true},
		{"en", "zh-CN", false},
		{"pt_BR", "pt", true},
		{"pt-BR", "pt-PT", false},
		{"pt-BR", "pt-br", true},
		{"es-MX", "es-ES", false},
		{"es", "es-419", true},
	} {
		if got := Same(c.a, c.b); got != c.same {
			t.Errorf("Same(%q, %q) = %v, want %v", c.a, c.b, got, c.same)
		}
	}
}

// TestCanonical 检查标签的规范化：大小写和下划线按规范写法，不合法的标签不接受。
func TestCanonical(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"hi-latn": "hi-Latn", "zh_CN": "zh-CN", " EN ": "en", "pt-br": "pt-BR"} {
		if got, ok := Canonical(in); !ok || got != want {
			t.Errorf("Canonical(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "und", "not a tag", "x"} {
		if got, ok := Canonical(in); ok {
			t.Errorf("Canonical(%q) = %q，应当不接受", in, got)
		}
	}
}

// TestMatchUIAndAcceptLanguage 检查界面语言匹配和浏览器 Accept-Language 提取。
func TestMatchUIAndAcceptLanguage(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"zh-CN":             ZhCN,
		"zh-Hans":           ZhCN,
		"zh-TW":             ZhCN,
		"en":                En,
		"pt-BR":             En,
		"hi-Latn":           En,
		"pt-BR,pt;q=0.9":    En,
		"zh-CN,zh;q=0.9":    ZhCN,
		"":                  En,
		"not-a-valid-value": En,
	} {
		if got := MatchUI(in); got != want {
			t.Errorf("MatchUI(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"pt-BR,pt;q=0.9,en;q=0.8": "pt-BR",
		"hi-Latn,en;q=0.8":        "hi-Latn",
		"zh-CN,zh;q=0.9":          "zh-CN",
		"":                        "",
	} {
		if got := FromAcceptLanguage(in); got != want {
			t.Errorf("FromAcceptLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
