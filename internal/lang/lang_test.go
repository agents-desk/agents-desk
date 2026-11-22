package lang

import "testing"

// TestSame 检查语言的比较：主语言和书写方式相同才算同一种语言，地区不算，中文按地区推断简体和繁体。
func TestSame(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"zh-CN", "zh-Hans", true}, {"zh-CN", "zh", true}, {"zh-TW", "zh-Hans", false}, {"zh-Hant", "zh-HK", true},
		{"en", "en-US", true}, {"hi", "hi-Latn", false}, {"hi-Latn", "hi-Latn", true}, {"en", "zh-CN", false}, {"pt_BR", "pt", true},
	} {
		if got := Same(c.a, c.b); got != c.same {
			t.Errorf("Same(%q, %q) = %v", c.a, c.b, got)
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
