package channel

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrInvalidPublicCode 表示网页商务链接自定义短码格式无效。
	ErrInvalidPublicCode = errorx.Define(errorx.InvalidArgument, "channel.invalid_public_code")
	// ErrPublicCodeTaken 表示网页商务链接短码已被其他渠道占用。
	ErrPublicCodeTaken = errorx.Define(errorx.Conflict, "channel.public_code_taken")
	// ErrInvalidTranslationTone 表示渠道翻译语气取值无效。
	ErrInvalidTranslationTone = errorx.Define(errorx.InvalidArgument, "channel.invalid_translation_tone")
)

// publicCodePattern 约束可读短码：3 到 40 位小写字母、数字和连字符，且首尾必须为字母或数字。
var publicCodePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])?$`)

// publicCodeAlphabet 去掉容易混淆的字符（0/o/1/l/i），生成易读的默认短码。
const publicCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// BusinessPolicy 是所有商务渠道（网页商务链接、Telegram 个人号及后续扩展渠道）共享的通用业务策略，
// 保存在 channels.settings.business 中，避免每个连接器重复实现协作、标签与翻译策略。
type BusinessPolicy struct {
	DefaultCollaboratorIDs []string `json:"default_collaborator_ids,omitempty" maxItems:"20" doc:"新商务会话建立时自动拉入协作的成员 ID 列表"`
	DefaultTagIDs          []string `json:"default_tag_ids,omitempty" maxItems:"20" doc:"经该渠道进入的新联系人自动添加的标签 ID 列表"`
	TranslationTone        string   `json:"translation_tone,omitempty" enum:",friendly,neutral,formal" doc:"渠道级翻译语气覆写；留空表示继承工作区商务默认语气"`
	AutoTranslate          *bool    `json:"auto_translate,omitempty" doc:"渠道级自动翻译开关覆写；为空表示继承工作区设置"`
}

// NormalizeBusinessPolicy 校验并规范化通用商务策略：去重非空 UUID、校验翻译语气取值。
func NormalizeBusinessPolicy(in BusinessPolicy) (BusinessPolicy, error) {
	out := BusinessPolicy{
		DefaultCollaboratorIDs: uniqueUUIDs(in.DefaultCollaboratorIDs),
		DefaultTagIDs:          uniqueUUIDs(in.DefaultTagIDs),
		TranslationTone:        strings.TrimSpace(in.TranslationTone),
		AutoTranslate:          in.AutoTranslate,
	}
	switch out.TranslationTone {
	case "", "friendly", "neutral", "formal":
	default:
		return BusinessPolicy{}, ErrInvalidTranslationTone.New(errorx.Field("business.translation_tone"))
	}
	return out, nil
}

// BusinessPolicyOf 从渠道 settings JSONB 中解析通用商务策略；缺失时返回零值。
func BusinessPolicyOf(settings map[string]any) BusinessPolicy {
	if len(settings) == 0 {
		return BusinessPolicy{}
	}
	raw, ok := settings["business"]
	if !ok || raw == nil {
		return BusinessPolicy{}
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return BusinessPolicy{}
	}
	var p BusinessPolicy
	if err := json.Unmarshal(b, &p); err != nil {
		return BusinessPolicy{}
	}
	return p
}

// SetBusinessPolicy 将通用商务策略写入渠道 settings map 的 business 字段。
func SetBusinessPolicy(settings map[string]any, p BusinessPolicy) map[string]any {
	if settings == nil {
		settings = map[string]any{}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return settings
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return settings
	}
	settings["business"] = m
	return settings
}

// GeneratePublicCode 生成 10 位随机可读短码，用作网页商务链接默认 /c/{code} 访问标识。
func GeneratePublicCode() string {
	var b strings.Builder
	b.Grow(10)
	max := big.NewInt(int64(len(publicCodeAlphabet)))
	for range 10 {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand 正常不会失败，回退至 UUIDv7 片段保证非空
			return strings.ReplaceAll(database.NewID(), "-", "")[:10]
		}
		b.WriteByte(publicCodeAlphabet[n.Int64()])
	}
	return b.String()
}

// NormalizePublicCode 校验并规范化网页商务链接短码（小写去空白），不合法时返回 ErrInvalidPublicCode。
func NormalizePublicCode(raw string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(raw))
	if !publicCodePattern.MatchString(code) {
		return "", ErrInvalidPublicCode.New(errorx.Field("public_code"))
	}
	return code, nil
}

// uniqueUUIDs 过滤无效字符串并保持原顺序去重。
func uniqueUUIDs(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
