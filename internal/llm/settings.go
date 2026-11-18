// Package llm 是模型调用：每种用途配置一个 OpenAI 兼容的接口（地址、密钥）和模型名，不同用途可以使用不同的接口。
// 这些是平台设置，由平台管理员在管理员端修改。
package llm

import (
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/settings"
)

// Endpoint 是一种用途使用的接口和模型。地址为空表示还没有配置这种用途。
type Endpoint struct {
	BaseURL string `json:"base_url" maxLength:"500" pattern:"^(https?://\\S+)?$" doc:"OpenAI 兼容接口的地址，http:// 或 https:// 开头，例如 https://gateway.example.com/v1"`
	APIKey  string `json:"api_key" maxLength:"500" doc:"接口的密钥，明文保存"`
	Model   string `json:"model" maxLength:"200" doc:"模型名"`
}

// Settings 是各用途的模型接口。第 1 阶段只用到翻译和语言识别，其他用途用到时再加字段。
// 字段不带 omitempty，见 settings 包的说明。
type Settings struct {
	Translation       Endpoint `json:"translation" doc:"翻译"`
	LanguageDetection Endpoint `json:"language_detection" doc:"识别语言和书写方式"`
}

// NewGroup 返回平台的模型接口设置，名称 llm 保存在 settings 表中，发布后保持固定。
func NewGroup(db bun.IDB) *settings.Group[Settings] {
	return settings.New(db, "llm", Settings{})
}

// SettingsBody 是读取和保存设置的请求体和响应体。
type SettingsBody struct {
	Body Settings
}

// changes 返回从 old 到 updated 修改了的字段，例如 translation.model；密钥只记字段名，不记值。
func changes(old, updated Settings) []string {
	var changed []string
	for _, p := range []struct {
		name     string
		old, new Endpoint
	}{
		{"translation", old.Translation, updated.Translation},
		{"language_detection", old.LanguageDetection, updated.LanguageDetection},
	} {
		if p.old.BaseURL != p.new.BaseURL {
			changed = append(changed, p.name+".base_url")
		}
		if p.old.APIKey != p.new.APIKey {
			changed = append(changed, p.name+".api_key")
		}
		if p.old.Model != p.new.Model {
			changed = append(changed, p.name+".model")
		}
	}
	return changed
}
