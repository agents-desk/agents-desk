// Package llm 是模型调用：每种调用类型配置一个模型接口（地址、密钥）和模型名，不同调用类型可以使用不同的接口和供应商。
// 接口的协议不预设为 OpenAI 兼容：每种调用类型的调用代码按它的接口实现，例如重排序使用 rerank 接口。
// 这些是平台设置，由平台管理员在管理员端修改。
package llm

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/settings"
)

// Endpoint 是一种调用类型使用的接口和模型。地址为空表示还没有配置这种调用类型。
type Endpoint struct {
	BaseURL string `json:"base_url" maxLength:"500" pattern:"^(https?://\\S+)?$" doc:"接口的地址，http:// 或 https:// 开头，例如 https://api.example.com/v1"`
	APIKey  string `json:"api_key" maxLength:"500" doc:"接口的密钥，明文保存"`
	Model   string `json:"model" maxLength:"200" doc:"模型名"`
}

// Settings 是各调用类型的模型接口。第 1 阶段只用到翻译：一次调用同时识别原文的语言和书写方式并给出译文，
// 语言识别不单独配置。其他调用类型（向量、重排序、语音识别等）用到时再加字段。
// 字段不带 omitempty，见 settings 包的说明。
type Settings struct {
	Translation Endpoint `json:"translation" doc:"翻译：在不同语言之间互译文本，同时识别原文的语言和书写方式"`
}

// NewGroup 返回平台的模型接口设置，名称 llm 保存在 settings 表中，发布后保持固定。
func NewGroup(db bun.IDB) *settings.Group[Settings] {
	return settings.New(db, "llm", Settings{})
}

// SettingsBody 是读取设置的响应体。
type SettingsBody struct {
	Body Settings
}

// CallType 是模型的一种调用类型，对应 Settings 中的一个字段。
type CallType string

const (
	Translation CallType = "translation" // 翻译，同时识别原文的语言和书写方式
)

// CallTypes 是全部调用类型，显示文字是翻译文件中 llm.call_type.<调用类型> 的消息。
var CallTypes = enum.New("llm.call_type", Translation)

// Schema 让 OpenAPI 中的 CallType 只接受上面的取值，并带有显示文字。
func (CallType) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, CallTypes) }

// endpoint 返回 s 中调用类型 t 的接口。t 必须是 CallTypes 中的取值，接口的路径参数已经校验过。
func (s *Settings) endpoint(t CallType) *Endpoint {
	switch t {
	case Translation:
		return &s.Translation
	}
	panic("llm: unknown call type " + string(t))
}

// changes 返回从 old 到 updated 修改了的字段，例如 model；密钥只记字段名，不记值。
func changes(old, updated Endpoint) []string {
	var changed []string
	if old.BaseURL != updated.BaseURL {
		changed = append(changed, "base_url")
	}
	if old.APIKey != updated.APIKey {
		changed = append(changed, "api_key")
	}
	if old.Model != updated.Model {
		changed = append(changed, "model")
	}
	return changed
}
