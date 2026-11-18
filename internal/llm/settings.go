// Package llm 是模型调用：agents-desk 通过一个 OpenAI 兼容的接口调用模型，每种用途配置一个模型名。
// 接口的地址、密钥和各用途的模型名是平台设置，由平台管理员在管理员端修改，密钥加密保存。
package llm

import (
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/settings"
)

// Models 是各用途使用的模型名，为空表示还没有配置这种用途。第 1 阶段只用到翻译和语言识别，其他用途用到时再加。
type Models struct {
	Translation       string `json:"translation" maxLength:"200" doc:"翻译使用的模型名"`
	LanguageDetection string `json:"language_detection" maxLength:"200" doc:"识别语言和书写方式使用的模型名"`
}

// Settings 是保存的模型接口设置。密钥以密文保存，接口不返回它。字段不带 omitempty，见 settings 包的说明。
type Settings struct {
	BaseURL         string `json:"base_url" maxLength:"500"`
	APIKeyEncrypted string `json:"api_key_encrypted"` // crypt.Encrypt 的结果，空表示没有设置密钥
	Models          Models `json:"models"`
}

// NewGroup 返回平台的模型接口设置，名称 llm 保存在 settings 表中，发布后保持固定。
func NewGroup(db bun.IDB) *settings.Group[Settings] {
	return settings.New(db, "llm", Settings{})
}

// View 是接口中的模型接口设置：密钥只告诉调用方是否已设置和末尾几位。
type View struct {
	BaseURL    string `json:"base_url" doc:"OpenAI 兼容接口的地址，例如 https://gateway.example.com/v1"`
	APIKeySet  bool   `json:"api_key_set" doc:"是否已经设置了密钥"`
	APIKeyHint string `json:"api_key_hint" doc:"密钥的末尾几位，用来确认是哪一个密钥；密钥太短时为空"`
	Models     Models `json:"models"`
}

// ViewOutput 是读取或保存设置的响应。
type ViewOutput struct {
	Body View
}

// hint 返回密钥末尾 4 位的提示，例如 …a1b2；密钥不到 12 位时返回空字符串，避免泄露太多。
func hint(key string) string {
	if len(key) < 12 {
		return ""
	}
	return "…" + key[len(key)-4:]
}
