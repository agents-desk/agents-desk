// Package llm 是模型调用：平台配置一个统一的 OpenAI 兼容模型网关（如 OmiRouter）连接，
// 以及各调用类型使用的逻辑模型名。底层通过 go-tenon/tenon/ai 发起标准协议请求，
// 并由统一的 http.RoundTripper 注入会话粘性头、采集网关计量响应头并记录每次调用。
package llm

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/settings"
)

// Gateway 是统一模型网关（例如 OmiRouter）的连接配置。地址为空表示未启用模型能力。
type Gateway struct {
	BaseURL string `json:"base_url" maxLength:"500" pattern:"^(https?://\\S+)?$" doc:"统一模型网关地址，http:// 或 https:// 开头，例如 https://gateway.example.com/v1"`
	APIKey  string `json:"api_key" maxLength:"500" doc:"网关密钥（Virtual Key），明文保存"`
}

// Models 是各业务调用类型在网关中对应的逻辑模型名。模型名为空表示未启用该调用类型。
// 字段不带 omitempty，见 settings 包的说明。
type Models struct {
	Translation string `json:"translation" maxLength:"200" doc:"翻译：在不同语言之间互译文本，同时识别原文的语言和书写方式"`
}

// Settings 是平台的模型网关与各调用类型模型配置。
// 字段不带 omitempty，见 settings 包的说明。
type Settings struct {
	Gateway Gateway `json:"gateway" doc:"统一模型网关连接"`
	Models  Models  `json:"models" doc:"各调用类型使用的逻辑模型名"`
}

// NewGroup 返回平台的模型配置组，名称 llm 保存在 settings 表中，发布后保持固定。
func NewGroup(db bun.IDB) *settings.Group[Settings] {
	return settings.New(db, "llm", Settings{})
}

// SettingsBody 是读取或保存设置的响应体。
type SettingsBody struct {
	Body Settings
}

// CallType 是模型的一种调用类型，对应 Models 中的一个字段。
type CallType string

const (
	Translation CallType = "translation" // 翻译，同时识别原文的语言和书写方式
)

// CallTypes 是全部调用类型，显示文字是翻译文件中 llm.call_type.<调用类型> 的消息。
var CallTypes = enum.New("llm.call_type", enum.Item(Translation))

// Schema 让 OpenAPI 中的 CallType 只接受上面的取值，并带有显示文字。
func (CallType) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, CallTypes) }

// model 返回 s 中调用类型 t 配置的逻辑模型名。
func (s *Settings) model(t CallType) string {
	switch t {
	case Translation:
		return s.Models.Translation
	}
	panic("llm: unknown call type " + string(t))
}

// settingsChanges 返回从 old 到 updated 修改了的字段名；密钥只记字段名，不记值。
func settingsChanges(old, updated Settings) []string {
	var changed []string
	if old.Gateway.BaseURL != updated.Gateway.BaseURL {
		changed = append(changed, "gateway.base_url")
	}
	if old.Gateway.APIKey != updated.Gateway.APIKey {
		changed = append(changed, "gateway.api_key")
	}
	if old.Models.Translation != updated.Models.Translation {
		changed = append(changed, "models.translation")
	}
	return changed
}
