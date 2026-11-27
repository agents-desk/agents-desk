package translation

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"
)

// ErrDuplicateTerm 是术语表中有重复的词（不区分大小写）。
var ErrDuplicateTerm = errorx.Define(errorx.InvalidArgument, "translation.duplicate_term")

// SettingsDTO 是接口中的翻译设置：工作区的语气、术语表、翻译是否可用，以及工作区实际活跃的语言列表。
type SettingsDTO struct {
	Settings
	Enabled            bool     `json:"enabled" doc:"翻译是否可用：平台设置中配置了翻译的模型接口。不可用时收到的消息不翻译，回复按原文发出"`
	WorkspaceLanguages []string `json:"workspace_languages" nullable:"false" doc:"当前工作区中实际出现过的活跃语言标签（成员与联系人语言去重，按使用频次降序），供前端选择器和筛选器动态置顶"`
}

// SettingsOutput 是读取翻译设置的响应。
type SettingsOutput struct {
	Body SettingsDTO
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
	db         *bun.DB
	settings   *settings.Group[Settings]
	translator *Translator
}

// NewSettingsActions 返回翻译设置的读取和保存；translator 告诉页面翻译是否可用。
func NewSettingsActions(db *bun.DB, s *settings.Group[Settings], translator *Translator) *SettingsActions {
	return &SettingsActions{db: db, settings: s, translator: translator}
}

// Get 返回 sub 所在工作区的翻译设置、翻译是否可用，以及工作区当前活跃语言列表。
func (a *SettingsActions) Get(ctx context.Context, sub identity.Subject) (*SettingsDTO, error) {
	s, err := a.settings.Tenant(sub.Tenant).Get(ctx)
	if err != nil {
		return nil, err
	}
	enabled, err := a.translator.EnabledFor(ctx, sub.Tenant)
	if err != nil {
		return nil, err
	}
	langs, err := workspaceLanguages(ctx, a.db, sub.Tenant)
	if err != nil {
		return nil, err
	}
	return &SettingsDTO{Settings: s, Enabled: enabled, WorkspaceLanguages: langs}, nil
}

// workspaceLanguages 聚合工作区 tenant 中成员、联系人与会话实际出现过的非空语言标签，按出现次数降序排列。
func workspaceLanguages(ctx context.Context, db bun.IDB, tenant string) ([]string, error) {
	var rows []struct {
		Language string `bun:"language"`
		Count    int64  `bun:"cnt"`
	}
	err := db.NewRaw(`
		SELECT language, SUM(cnt) AS cnt FROM (
			SELECT m.language, COUNT(DISTINCT m.id) AS cnt
			FROM members m
			JOIN authz_assignments a ON a.subject_type = 'member' AND a.subject_id = m.id::text AND a.tenant = ?
			WHERE m.disabled_at IS NULL AND m.language <> ''
			GROUP BY m.language
			UNION ALL
			SELECT c.language, COUNT(*) AS cnt
			FROM contacts c
			WHERE c.tenant_id = ? AND c.language <> ''
			GROUP BY c.language
			UNION ALL
			SELECT conv.language, COUNT(*) AS cnt
			FROM conversations conv
			WHERE conv.tenant_id = ? AND conv.language <> ''
			GROUP BY conv.language
		) t
		GROUP BY language
		ORDER BY cnt DESC, language ASC
		LIMIT 50`, tenant, tenant, tenant).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load workspace languages")
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if l := strings.TrimSpace(r.Language); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// SaveTone 保存 sub 所在工作区的语气，记录操作日志。
func (a *SettingsActions) SaveTone(ctx context.Context, sub identity.Subject, tone Tone) (*SettingsDTO, error) {
	return a.save(ctx, sub, "tone", func(s *Settings) error {
		s.Tone = tone
		return nil
	})
}

// SaveGlossary 保存 sub 所在工作区的术语表（整张表），记录操作日志。词去掉首尾空白，空的译法去掉；
// 有重复的词（不区分大小写）时返回 ErrDuplicateTerm。
func (a *SettingsActions) SaveGlossary(ctx context.Context, sub identity.Subject, glossary []Term) (*SettingsDTO, error) {
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
func (a *SettingsActions) save(ctx context.Context, sub identity.Subject, part string, change func(*Settings) error) (*SettingsDTO, error) {
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
