// Package llm 是模型调用：平台配置一个统一的 OpenAI 兼容模型网关（如 OmiRouter）连接，
// 各业务调用类型使用的逻辑模型名，以及单工作区默认与覆盖预算熔断上限。
// 底层通过 go-tenon/tenon/ai 发起标准协议请求，并由统一的 http.RoundTripper 执行预算熔断、注入会话粘性头、采集计量响应头并记录每次调用。
package llm

import (
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/settings"
)

// Gateway 是统一模型网关（例如 OmiRouter）的连接配置。地址为空表示未启用模型能力。
type Gateway struct {
	BaseURL string `json:"base_url" maxLength:"500" pattern:"^(https?://\\S+)?$" doc:"统一模型网关地址，http:// 或 https:// 开头，例如 https://gateway.example.com/v1"`
	APIKey  string `json:"api_key" maxLength:"500" format:"password" settings:"secret" doc:"网关密钥（Virtual Key），落库时自动加密保存"`
}

// Models 是各业务调用类型在网关中对应的逻辑模型名。模型名为空表示未启用该调用类型。
// 字段不带 omitempty，见 settings 包的说明。
type Models struct {
	Chat        string `json:"chat" maxLength:"200" doc:"AI 对话：机器人与 Agent 主推理和工具调用模型"`
	FastRouter  string `json:"fast_router" maxLength:"200" doc:"快速路由：低延迟分类决策与意图分流模型"`
	Translation string `json:"translation" maxLength:"200" doc:"翻译：在不同语言之间互译文本，同时识别原文的语言和书写方式"`
	Embedding   string `json:"embedding" maxLength:"200" doc:"向量化：知识库文档与查询向量编码模型"`
	Rerank      string `json:"rerank" maxLength:"200" doc:"重排：检索候选切片语义重排序模型"`
	Transcribe  string `json:"transcribe" maxLength:"200" doc:"语音识别：将语音消息转写为文本"`
}

// Budget 是单工作区的模型调用费用与 token 用量熔断上限，0 表示不限。
// 字段不带 omitempty，见 settings 包的说明。
type Budget struct {
	DailyCostUSD     float64 `json:"daily_cost_usd" minimum:"0" maximum:"100000" doc:"单工作区每日费用上限（美元），0 表示不限"`
	MonthlyCostUSD   float64 `json:"monthly_cost_usd" minimum:"0" maximum:"1000000" doc:"单工作区每月费用上限（美元），0 表示不限"`
	DailyTotalTokens int64   `json:"daily_total_tokens" minimum:"0" maximum:"10000000000" doc:"单工作区每日总 token 上限（输入+输出），0 表示不限"`
}

// TenantBudget 是管理员为单个工作区覆盖的模型预算上限；指针为 nil 表示跟随平台默认配置，显式设为 0 表示取消该项限制。
// 字段不带 omitempty，见 settings 包的说明。
type TenantBudget struct {
	DailyCostUSD     *float64 `json:"daily_cost_usd" minimum:"0" maximum:"100000" doc:"覆写工作区每日费用上限（美元）；留空跟随平台默认，0 表示不限"`
	MonthlyCostUSD   *float64 `json:"monthly_cost_usd" minimum:"0" maximum:"1000000" doc:"覆写工作区每月费用上限（美元）；留空跟随平台默认，0 表示不限"`
	DailyTotalTokens *int64   `json:"daily_total_tokens" minimum:"0" maximum:"10000000000" doc:"覆写工作区每日总 token 上限；留空跟随平台默认，0 表示不限"`
}

// Settings 是平台的模型网关、各调用类型模型与默认工作区预算配置。
// 字段不带 omitempty，见 settings 包的说明。
type Settings struct {
	Gateway       Gateway `json:"gateway" doc:"统一模型网关连接"`
	Models        Models  `json:"models" doc:"各调用类型使用的逻辑模型名"`
	DefaultBudget Budget  `json:"default_budget" doc:"默认单工作区模型用量与费用熔断上限，0 表示不限"`
}

// NewGroup 返回平台的模型配置组，名称 llm 保存在 settings 表中，密钥字段经 keys 透明加密存储。
func NewGroup(db bun.IDB, keys *crypt.Crypt) *settings.Group[Settings] {
	return settings.New(db, "llm", Settings{}, settings.WithCrypt(keys))
}

// NewTenantBudgetGroup 返回工作区级的模型预算覆写配置组，名称 llm_budget 保存在 settings 表中。
func NewTenantBudgetGroup(db bun.IDB) *settings.Group[TenantBudget] {
	return settings.New(db, "llm_budget", TenantBudget{})
}

// SettingsBody 是读取或保存设置的响应体。
type SettingsBody struct {
	Body Settings
}

// CallType 是模型的一种调用类型，对应 Models 中的一个字段。
type CallType string

const (
	Chat        CallType = "chat"        // AI 对话与 Agent 主推理
	FastRouter  CallType = "fast_router" // 快速路由与分类决策
	Translation CallType = "translation" // 翻译，同时识别原文的语言和书写方式
	Embedding   CallType = "embedding"   // 向量化检索
	Rerank      CallType = "rerank"      // 检索结果重排
	Transcribe  CallType = "transcribe"  // 语音转文字
)

// CallTypes 是全部调用类型，显示文字是翻译文件中 llm.call_type.<调用类型> 的消息。
var CallTypes = enum.New("llm.call_type",
	enum.Item(Chat), enum.Item(FastRouter), enum.Item(Translation),
	enum.Item(Embedding), enum.Item(Rerank), enum.Item(Transcribe))

// Schema 让 OpenAPI 中的 CallType 只接受上面的取值，并带有显示文字。
func (CallType) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, CallTypes) }

// model 返回 s 中调用类型 t 配置的逻辑模型名。
func (s *Settings) model(t CallType) string {
	switch t {
	case Chat:
		return s.Models.Chat
	case FastRouter:
		return s.Models.FastRouter
	case Translation:
		return s.Models.Translation
	case Embedding:
		return s.Models.Embedding
	case Rerank:
		return s.Models.Rerank
	case Transcribe:
		return s.Models.Transcribe
	}
	panic("llm: unknown call type " + string(t))
}

// resolveBudget 将平台默认预算与工作区覆写合并为最终生效阈值。
func resolveBudget(def Budget, ov TenantBudget) Budget {
	eff := def
	if ov.DailyCostUSD != nil {
		eff.DailyCostUSD = *ov.DailyCostUSD
	}
	if ov.MonthlyCostUSD != nil {
		eff.MonthlyCostUSD = *ov.MonthlyCostUSD
	}
	if ov.DailyTotalTokens != nil {
		eff.DailyTotalTokens = *ov.DailyTotalTokens
	}
	return eff
}

// formatBudgetDiff 返回覆写配置从 old 到 updated 修改的字段列表，用于审计日志。
func formatBudgetDiff(old, updated TenantBudget) []string {
	var changed []string
	eqFloat := func(a, b *float64) bool {
		if a == nil || b == nil {
			return a == b
		}
		return *a == *b
	}
	eqInt := func(a, b *int64) bool {
		if a == nil || b == nil {
			return a == b
		}
		return *a == *b
	}
	if !eqFloat(old.DailyCostUSD, updated.DailyCostUSD) {
		changed = append(changed, fmt.Sprintf("daily_cost_usd=%v", ptrFloatStr(updated.DailyCostUSD)))
	}
	if !eqFloat(old.MonthlyCostUSD, updated.MonthlyCostUSD) {
		changed = append(changed, fmt.Sprintf("monthly_cost_usd=%v", ptrFloatStr(updated.MonthlyCostUSD)))
	}
	if !eqInt(old.DailyTotalTokens, updated.DailyTotalTokens) {
		changed = append(changed, fmt.Sprintf("daily_total_tokens=%v", ptrIntStr(updated.DailyTotalTokens)))
	}
	return changed
}

func ptrFloatStr(p *float64) string {
	if p == nil {
		return "default"
	}
	return fmt.Sprintf("%.4g", *p)
}

func ptrIntStr(p *int64) string {
	if p == nil {
		return "default"
	}
	return fmt.Sprintf("%d", *p)
}
