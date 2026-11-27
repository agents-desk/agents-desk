package widget

import (
	"maps"
	"slices"
	"strings"

	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/lang"
)

// SupportedLanguages 是网页聊天窗口内置的界面语种列表，与前端 apps/widget/src/i18n.ts 严格一致；第一项为全局回退语言。
var SupportedLanguages = []string{"en", "zh-CN", "zh-TW", "hi", "hi-Latn"}

// LocalizedCopy 是单一语言下渠道面向访客或外部合作方展示的全部自定义文案。
type LocalizedCopy struct {
	Title        string   `json:"title,omitempty" maxLength:"60" doc:"对外窗口标题；为空时回退渠道内部名称"`
	Greeting     string   `json:"greeting,omitempty" maxLength:"500" doc:"聊天窗口顶部的欢迎问候语；为空时显示界面默认问候语"`
	OwnerTitle   string   `json:"owner_title,omitempty" maxLength:"60" doc:"商务链接对外名片头衔覆写"`
	OwnerCompany string   `json:"owner_company,omitempty" maxLength:"80" doc:"商务链接对外公司或品牌名覆写"`
	OwnerBio     string   `json:"owner_bio,omitempty" maxLength:"240" doc:"商务链接对外简介覆写"`
	AwayMessage  string   `json:"away_message,omitempty" maxLength:"300" doc:"商务链接成员处于离开状态时的提示语"`
	Starters     []string `json:"starters,omitempty" maxItems:"5" doc:"商务链接快捷开场话题（最多 5 条）"`
}

// Localization 是网页渠道的语言策略与多语言自定义文案目录。
type Localization struct {
	DefaultLanguage  string                   `json:"default_language" maxLength:"35" doc:"渠道主语言（BCP 47），作为缺省回退与 AI 翻译源语言"`
	EnabledLanguages []string                 `json:"enabled_languages,omitempty" maxItems:"30" doc:"访客可选语言白名单；为空表示开放全部内置界面语言"`
	AllowSwitch      bool                     `json:"allow_switch" doc:"是否在聊天窗口顶栏展示访客语言切换器"`
	Translations     map[string]LocalizedCopy `json:"translations" doc:"按规范 BCP 47 语言标签索引的自定义文案"`
}

// defaultLocalization 返回新建网页渠道时的默认多语言策略：默认语言优先取 defaultLang（空或非法时取 en），允许访客切换语言。
func defaultLocalization(defaultLang string) Localization {
	dl := lang.En
	if canon, ok := lang.Canonical(defaultLang); ok {
		dl = canon
	}
	return Localization{
		DefaultLanguage:  dl,
		EnabledLanguages: []string{},
		AllowSwitch:      true,
		Translations:     map[string]LocalizedCopy{},
	}
}

// normalizeLocalizedCopy 裁剪单语种自定义文案的空白与长度，非商务渠道自动清掉商务专属字段。
func normalizeLocalizedCopy(in LocalizedCopy, isBusiness bool) LocalizedCopy {
	out := LocalizedCopy{
		Title:    strings.TrimSpace(in.Title),
		Greeting: strings.TrimSpace(in.Greeting),
	}
	if !isBusiness {
		return out
	}
	out.OwnerTitle = strings.TrimSpace(in.OwnerTitle)
	out.OwnerCompany = strings.TrimSpace(in.OwnerCompany)
	out.OwnerBio = strings.TrimSpace(in.OwnerBio)
	out.AwayMessage = strings.TrimSpace(in.AwayMessage)
	if len(in.Starters) > 0 {
		out.Starters = normalizeStarters(in.Starters)
	}
	return out
}

// isEmpty 报告单语种自定义文案是否全部字段均为空。
func (c LocalizedCopy) isEmpty() bool {
	return c.Title == "" &&
		c.Greeting == "" &&
		c.OwnerTitle == "" &&
		c.OwnerCompany == "" &&
		c.OwnerBio == "" &&
		c.AwayMessage == "" &&
		len(c.Starters) == 0
}

// equal 报告两份单语种自定义文案是否完全一致。
func (c LocalizedCopy) equal(other LocalizedCopy) bool {
	return c.Title == other.Title &&
		c.Greeting == other.Greeting &&
		c.OwnerTitle == other.OwnerTitle &&
		c.OwnerCompany == other.OwnerCompany &&
		c.OwnerBio == other.OwnerBio &&
		c.AwayMessage == other.AwayMessage &&
		slices.Equal(c.Starters, other.Starters)
}

// normalizeLocalization 校验并规范化渠道多语言配置：
//   - DefaultLanguage 为空时回退为 "en"，非合法 BCP 47 标签返回 ErrInvalidLanguage；
//   - EnabledLanguages 规范化并去重，含非法标签返回 ErrInvalidLanguage；
//   - Translations 键规范化为标准 BCP 47 标签，空文案条目自动省略。
func normalizeLocalization(in Localization, isBusiness bool) (Localization, error) {
	defaultLang := strings.TrimSpace(in.DefaultLanguage)
	if defaultLang == "" {
		defaultLang = lang.En
	} else {
		canon, ok := lang.Canonical(defaultLang)
		if !ok {
			return Localization{}, ErrInvalidLanguage.New(errorx.Field("localization.default_language"), errorx.Public("language", in.DefaultLanguage))
		}
		defaultLang = canon
	}

	enabled := make([]string, 0, len(in.EnabledLanguages))
	seen := map[string]bool{}
	for _, raw := range in.EnabledLanguages {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		canon, ok := lang.Canonical(trimmed)
		if !ok {
			return Localization{}, ErrInvalidLanguage.New(errorx.Field("localization.enabled_languages"), errorx.Public("language", raw))
		}
		if !seen[canon] {
			seen[canon] = true
			enabled = append(enabled, canon)
		}
	}

	translations := make(map[string]LocalizedCopy, len(in.Translations))
	for rawKey, rawCopy := range in.Translations {
		canon, ok := lang.Canonical(rawKey)
		if !ok {
			return Localization{}, ErrInvalidLanguage.New(errorx.Field("localization.translations"), errorx.Public("language", rawKey))
		}
		cleaned := normalizeLocalizedCopy(rawCopy, isBusiness)
		if cleaned.isEmpty() {
			continue
		}
		translations[canon] = cleaned
	}

	return Localization{
		DefaultLanguage:  defaultLang,
		EnabledLanguages: enabled,
		AllowSwitch:      in.AllowSwitch,
		Translations:     translations,
	}, nil
}

// localizationEqual 报告两份渠道多语言配置是否完全相等，用于变更审计。
func localizationEqual(a, b Localization) bool {
	if a.DefaultLanguage != b.DefaultLanguage || a.AllowSwitch != b.AllowSwitch {
		return false
	}
	if !slices.Equal(a.EnabledLanguages, b.EnabledLanguages) {
		return false
	}
	if len(a.Translations) != len(b.Translations) {
		return false
	}
	for k, va := range a.Translations {
		vb, ok := b.Translations[k]
		if !ok || !va.equal(vb) {
			return false
		}
	}
	return true
}

// localization 读取 Settings 中规范化后的多语言配置，确保非空映射和有效默认语言。
func (s Settings) localization() Localization {
	loc := s.Localization
	if loc.DefaultLanguage == "" {
		return defaultLocalization(lang.En)
	}
	if loc.EnabledLanguages == nil {
		loc.EnabledLanguages = []string{}
	}
	if loc.Translations == nil {
		loc.Translations = map[string]LocalizedCopy{}
	}
	return loc
}

// availableLanguages 返回供访客语言切换器展示的可选语言列表：
// 若配置了 EnabledLanguages 白名单则按白名单顺序返回；否则返回内置语种列表并追加已配置翻译的额外语言。
func availableLanguages(loc Localization) []string {
	if len(loc.EnabledLanguages) > 0 {
		return slices.Clone(loc.EnabledLanguages)
	}
	out := make([]string, 0, len(SupportedLanguages)+len(loc.Translations))
	seen := map[string]bool{}
	add := func(tag string) {
		if tag == "" || seen[tag] {
			return
		}
		seen[tag] = true
		out = append(out, tag)
	}
	for _, l := range SupportedLanguages {
		add(l)
	}
	for _, k := range slices.Sorted(maps.Keys(loc.Translations)) {
		add(k)
	}
	return out
}

// lookupTranslation 在 translations 中按精确标签或 lang.Same 规则查找最接近的条目。
func lookupTranslation(translations map[string]LocalizedCopy, target string) (string, LocalizedCopy, bool) {
	if target == "" || len(translations) == 0 {
		return "", LocalizedCopy{}, false
	}
	if canon, ok := lang.Canonical(target); ok {
		if hit, exists := translations[canon]; exists {
			return canon, hit, true
		}
		for _, k := range slices.Sorted(maps.Keys(translations)) {
			if lang.Same(k, canon) {
				return k, translations[k], true
			}
		}
	}
	return "", LocalizedCopy{}, false
}

// resolveLocalizedCopy 按请求语言解析渠道自定义文案，支持字段级回退：
// 精确匹配 requestedLang → 同语言/脚本匹配 → 渠道主语言 DefaultLanguage。
func resolveLocalizedCopy(loc Localization, requestedLang string) (string, LocalizedCopy) {
	defaultLang := loc.DefaultLanguage
	if defaultLang == "" {
		defaultLang = lang.En
	}
	_, base, _ := lookupTranslation(loc.Translations, defaultLang)

	reqCanon, ok := lang.Canonical(requestedLang)
	if !ok {
		return defaultLang, base
	}
	hitKey, hit, found := lookupTranslation(loc.Translations, reqCanon)
	if !found {
		return reqCanon, base
	}
	merged := LocalizedCopy{
		Title:        firstNonEmpty(hit.Title, base.Title),
		Greeting:     firstNonEmpty(hit.Greeting, base.Greeting),
		OwnerTitle:   firstNonEmpty(hit.OwnerTitle, base.OwnerTitle),
		OwnerCompany: firstNonEmpty(hit.OwnerCompany, base.OwnerCompany),
		OwnerBio:     firstNonEmpty(hit.OwnerBio, base.OwnerBio),
		AwayMessage:  firstNonEmpty(hit.AwayMessage, base.AwayMessage),
		Starters:     hit.Starters,
	}
	if len(merged.Starters) == 0 {
		merged.Starters = base.Starters
	}
	return hitKey, merged
}

// firstNonEmpty 返回两个字符串中第一个非空值。
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
