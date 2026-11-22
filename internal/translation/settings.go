package translation

import (
	"context"
	"strings"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"
)

// ErrDuplicateTerm 是术语表中有重复的词（不区分大小写）。
var ErrDuplicateTerm = errorx.Define(errorx.InvalidArgument, "translation.duplicate_term")

// SettingsOutput 是读取翻译设置的响应。
type SettingsOutput struct {
	Body Settings
}

// ToneInput 是保存语气的请求。
type ToneInput struct {
	Body struct {
		Tone Tone `json:"tone"`
	}
}

// GlossaryInput 是保存术语表的请求：整张表。
type GlossaryInput struct {
	Body struct {
		Glossary []Term `json:"glossary" maxItems:"500" nullable:"false"`
	}
}

// SettingsActions 是工作区的翻译设置：成员都能读取，有 tenant-settings:manage 的成员分别保存语气和术语表。
type SettingsActions struct {
	db       *bun.DB
	settings *settings.Group[Settings]
}

// NewSettingsActions 返回翻译设置的读取和保存。
func NewSettingsActions(db *bun.DB, s *settings.Group[Settings]) *SettingsActions {
	return &SettingsActions{db: db, settings: s}
}

// Get 返回 sub 所在工作区的翻译设置。
func (a *SettingsActions) Get(ctx context.Context, sub identity.Subject) (*Settings, error) {
	s, err := a.settings.Tenant(sub.Tenant).Get(ctx)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveTone 保存 sub 所在工作区的语气，记录操作日志。
func (a *SettingsActions) SaveTone(ctx context.Context, sub identity.Subject, tone Tone) (*Settings, error) {
	return a.save(ctx, sub, "tone", func(s *Settings) error {
		s.Tone = tone
		return nil
	})
}

// SaveGlossary 保存 sub 所在工作区的术语表（整张表），记录操作日志。词去掉首尾空白，空的译法去掉；
// 有重复的词（不区分大小写）时返回 ErrDuplicateTerm。
func (a *SettingsActions) SaveGlossary(ctx context.Context, sub identity.Subject, glossary []Term) (*Settings, error) {
	clean := make([]Term, 0, len(glossary))
	seen := map[string]bool{}
	for _, t := range glossary {
		t.Term = strings.TrimSpace(t.Term)
		key := strings.ToLower(t.Term)
		if seen[key] {
			return nil, ErrDuplicateTerm.New(errorx.Field("glossary"), errorx.Public("term", t.Term))
		}
		seen[key] = true
		translations := map[string]string{}
		for lang, v := range t.Translations {
			if v = strings.TrimSpace(v); v != "" && !t.Keep {
				translations[lang] = v
			}
		}
		t.Translations = translations
		clean = append(clean, t)
	}
	return a.save(ctx, sub, "glossary", func(s *Settings) error {
		s.Glossary = clean
		return nil
	})
}

// save 在一个事务中修改 sub 所在工作区的翻译设置的 part 部分，并记录操作日志。
func (a *SettingsActions) save(ctx context.Context, sub identity.Subject, part string, change func(*Settings) error) (*Settings, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := a.settings.WithDB(tx).Tenant(sub.Tenant).Update(ctx, change); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "translation_settings.updated", TargetType: "tenant", TargetID: sub.Tenant, Properties: map[string]any{"part": part},
		})
	})
	if err != nil {
		return nil, err
	}
	return a.Get(ctx, sub)
}

// GetHTTP 处理 GET /t/{slug}/api/settings/translation。
func (a *SettingsActions) GetHTTP(ctx context.Context, _ *struct{}) (*SettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	s, err := a.Get(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &SettingsOutput{Body: *s}, nil
}

// ToneHTTP 处理 PUT /t/{slug}/api/settings/translation/tone。
func (a *SettingsActions) ToneHTTP(ctx context.Context, in *ToneInput) (*SettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	s, err := a.SaveTone(ctx, sub, in.Body.Tone)
	if err != nil {
		return nil, err
	}
	return &SettingsOutput{Body: *s}, nil
}

// GlossaryHTTP 处理 PUT /t/{slug}/api/settings/translation/glossary。
func (a *SettingsActions) GlossaryHTTP(ctx context.Context, in *GlossaryInput) (*SettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	s, err := a.SaveGlossary(ctx, sub, in.Body.Glossary)
	if err != nil {
		return nil, err
	}
	return &SettingsOutput{Body: *s}, nil
}
