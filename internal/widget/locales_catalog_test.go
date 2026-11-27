package widget

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var widgetPlaceholderRE = regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`)

// extractWidgetPlaceholders 提取单条词条中的全部模板变量名并排序，用于校验多语言占位符一致。
func extractWidgetPlaceholders(s string) []string {
	matches := widgetPlaceholderRE.FindAllStringSubmatch(s, -1)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

// TestWidgetLocaleCatalogs 检查 apps/widget/src/locales/*.json 与后端 SupportedLanguages 严格一一对应，
// 且每个语种的词条键集合、非空值与模板占位符均与 en.json 完全一致。
func TestWidgetLocaleCatalogs(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "apps", "widget", "src", "locales")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read widget locales dir: %v", err)
	}
	var found []string
	catalogs := map[string]map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(entry.Name(), ".json")
		found = append(found, code)
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		var table map[string]string
		if err := json.Unmarshal(data, &table); err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		catalogs[code] = table
	}
	slices.Sort(found)
	wantLangs := slices.Clone(SupportedLanguages)
	slices.Sort(wantLangs)
	if !slices.Equal(found, wantLangs) {
		t.Fatalf("widget locale files = %v, want %v", found, wantLangs)
	}

	base := catalogs["en"]
	baseKeys := slices.Sorted(maps.Keys(base))
	if len(baseKeys) == 0 {
		t.Fatal("en.json has no messages")
	}
	for _, code := range SupportedLanguages {
		table := catalogs[code]
		keys := slices.Sorted(maps.Keys(table))
		if !slices.Equal(keys, baseKeys) {
			t.Errorf("%s.json keys mismatch en.json", code)
			continue
		}
		for _, k := range baseKeys {
			if strings.TrimSpace(table[k]) == "" {
				t.Errorf("%s.json[%q] is empty", code, k)
			}
			wantPH := extractWidgetPlaceholders(base[k])
			gotPH := extractWidgetPlaceholders(table[k])
			if !slices.Equal(gotPH, wantPH) {
				t.Errorf("%s.json[%q] placeholders = %v, want %v", code, k, gotPH, wantPH)
			}
		}
	}
}
