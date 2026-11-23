package locales_test

import (
	"io/fs"
	"maps"
	"slices"
	"testing"

	"go.yaml.in/yaml/v4"

	"agents-desk/internal/lang"
	"agents-desk/locales"
)

// TestEveryLanguageHasEveryMessage 检查每组翻译文件（服务端的 <语言>.yaml、各前端的 <前端>.<语言>.yaml）
// 在每种界面语言中都有同样的消息 ID：加一条消息时，每种语言都要写上它。
func TestEveryLanguageHasEveryMessage(t *testing.T) {
	t.Parallel()
	languages := lang.UILanguages
	for _, prefix := range []string{"", "web.", "admin."} {
		ids := map[string][]string{}
		for _, lang := range languages {
			data, err := fs.ReadFile(locales.FS, prefix+lang+".yaml")
			if err != nil {
				t.Fatal(err)
			}
			var messages map[string]any
			if err := yaml.Unmarshal(data, &messages); err != nil {
				t.Fatalf("%s%s.yaml: %v", prefix, lang, err)
			}
			ids[lang] = slices.Sorted(maps.Keys(messages))
		}
		want := ids[languages[0]]
		for _, lang := range languages[1:] {
			for _, id := range want {
				if !slices.Contains(ids[lang], id) {
					t.Errorf("%s%s.yaml is missing %s", prefix, lang, id)
				}
			}
			for _, id := range ids[lang] {
				if !slices.Contains(want, id) {
					t.Errorf("%s%s.yaml has %s, which %s%s.yaml lacks", prefix, lang, id, prefix, languages[0])
				}
			}
		}
		if len(want) == 0 {
			t.Errorf("%s%s.yaml has no messages", prefix, languages[0])
		}
	}
}
