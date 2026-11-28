package widget

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/lang"
	"agents-desk/internal/llm"
)

// CopyTranslator 是将一组渠道自定义文案字段按工作区术语表与语气翻译成多个目标语言的函数签名（由 internal/translation 注入，避免循环依赖）。
type CopyTranslator func(ctx context.Context, tenant, targetType, targetID, source string, targets []string, fields map[string]string) (map[string]map[string]string, error)

// TranslateCopyBody 是 AI 一键翻译渠道多语言文案的请求体。
type TranslateCopyBody struct {
	ChannelID       string        `json:"channel_id,omitempty" format:"uuid" doc:"可选：所属渠道 ID，用于记录模型调用关联目标"`
	SourceLanguage  string        `json:"source_language" minLength:"2" maxLength:"35" doc:"源语言（BCP 47），如 zh-CN 或 en"`
	TargetLanguages []string      `json:"target_languages" minItems:"1" maxItems:"20" doc:"要生成的目标语言列表（BCP 47）"`
	Copy            LocalizedCopy `json:"copy" doc:"待翻译的源语言自定义文案"`
}

// TranslateCopyInput 是 AI 翻译渠道文案的 HTTP 请求。
type TranslateCopyInput struct {
	Body TranslateCopyBody
}

// TranslateCopyDTO 是 AI 翻译渠道文案的响应体。
type TranslateCopyDTO struct {
	Translations map[string]LocalizedCopy `json:"translations" doc:"按规范 BCP 47 语言标签索引的译文"`
}

// TranslateCopyOutput 是 AI 翻译渠道文案的 HTTP 响应。
type TranslateCopyOutput struct {
	Body TranslateCopyDTO
}

// TranslateCopy 使用工作区翻译模型（含术语表与语气）将网页渠道或商务链接的主语言文案批量翻译为多个目标语言。
type TranslateCopy struct {
	translate CopyTranslator
}

// NewTranslateCopy 返回渠道文案 AI 翻译动作。
func NewTranslateCopy(translate CopyTranslator) *TranslateCopy {
	return &TranslateCopy{translate: translate}
}

// Handle 校验权限与语言标签后，将 b.Copy 中的全部非空文案字段一次翻译为 b.TargetLanguages。
// 调用者既无 channels:manage 也无 business_channels:use 时返回 ErrForbidden；语言标签非法返回 ErrInvalidLanguage；
// 平台未配置翻译模型返回 llm.ErrNotConfigured。
func (a *TranslateCopy) Handle(ctx context.Context, sub identity.Subject, b TranslateCopyBody) (*TranslateCopyDTO, error) {
	if sub.Type != identity.System &&
		!sub.Can(string(auth.ChannelsManage)) &&
		!sub.Can(string(auth.BusinessChannelsUse)) {
		return nil, ErrForbidden.New()
	}
	source, ok := lang.Canonical(b.SourceLanguage)
	if !ok {
		return nil, ErrInvalidLanguage.New(errorx.Field("source_language"), errorx.Public("language", b.SourceLanguage))
	}
	targets := make([]string, 0, len(b.TargetLanguages))
	seen := map[string]bool{}
	for _, raw := range b.TargetLanguages {
		canon, ok := lang.Canonical(raw)
		if !ok {
			return nil, ErrInvalidLanguage.New(errorx.Field("target_languages"), errorx.Public("language", raw))
		}
		if lang.Same(source, canon) || seen[canon] {
			continue
		}
		seen[canon] = true
		targets = append(targets, canon)
	}
	cleaned := normalizeLocalizedCopy(b.Copy, true)
	fields := map[string]string{}
	if cleaned.Title != "" {
		fields["title"] = cleaned.Title
	}
	if cleaned.Greeting != "" {
		fields["greeting"] = cleaned.Greeting
	}
	if cleaned.OwnerTitle != "" {
		fields["owner_title"] = cleaned.OwnerTitle
	}
	if cleaned.OwnerCompany != "" {
		fields["owner_company"] = cleaned.OwnerCompany
	}
	if cleaned.OwnerBio != "" {
		fields["owner_bio"] = cleaned.OwnerBio
	}
	if cleaned.AwayMessage != "" {
		fields["away_message"] = cleaned.AwayMessage
	}
	for i, s := range cleaned.Starters {
		fields[fmt.Sprintf("starter_%d", i)] = s
	}
	if len(targets) == 0 || len(fields) == 0 {
		return &TranslateCopyDTO{Translations: map[string]LocalizedCopy{}}, nil
	}
	if a.translate == nil {
		return nil, llm.ErrNotConfigured.New()
	}
	targetID := strings.TrimSpace(b.ChannelID)
	if targetID == "" {
		targetID = sub.Tenant
	}
	raw, err := a.translate(ctx, sub.Tenant, "channel_copy", targetID, source, targets, fields)
	if err != nil {
		return nil, err
	}
	out := make(map[string]LocalizedCopy, len(targets))
	for _, t := range targets {
		translated := raw[t]
		if len(translated) == 0 {
			continue
		}
		item := LocalizedCopy{
			Title:        strings.TrimSpace(translated["title"]),
			Greeting:     strings.TrimSpace(translated["greeting"]),
			OwnerTitle:   strings.TrimSpace(translated["owner_title"]),
			OwnerCompany: strings.TrimSpace(translated["owner_company"]),
			OwnerBio:     strings.TrimSpace(translated["owner_bio"]),
			AwayMessage:  strings.TrimSpace(translated["away_message"]),
		}
		if len(cleaned.Starters) > 0 {
			starters := make([]string, 0, len(cleaned.Starters))
			for i := range cleaned.Starters {
				if v := strings.TrimSpace(translated[fmt.Sprintf("starter_%d", i)]); v != "" {
					starters = append(starters, v)
				}
			}
			item.Starters = normalizeStarters(starters)
		}
		if !item.isEmpty() {
			out[t] = item
		}
	}
	return &TranslateCopyDTO{Translations: out}, nil
}

// HTTP 处理 POST /t/{slug}/api/widgets/translate-copy。
func (a *TranslateCopy) HTTP(ctx context.Context, in *TranslateCopyInput) (*TranslateCopyOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TranslateCopyOutput{Body: *dto}, nil
}
