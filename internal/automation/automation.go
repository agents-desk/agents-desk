// Package automation 是工作区的自动化引擎：按“触发 → 条件 → 动作”响应会话时间线中的事件。
//
// 第一版提供三条内置规则（告知回复时间、请线索留联系方式、关闭后请客户评价），管理员可以逐条开关；
// 内部已按声明式的规则表、条件 AST、动作执行和按 (rule_id, dedup_key) 幂等去重实现，
// 后续的按规则指派和可视化规则编辑直接在此扩展。
package automation

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrNotFound 是自动化规则不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "automation.not_found")
	// ErrForbidden 是调用者没有管理自动化的权限。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "automation.forbidden")
)

// Trigger 是触发自动化规则的会话事件种类。
type Trigger string

const (
	TriggerFirstMessage Trigger = "conversation.first_message" // 客户在会话中发来第一条消息
	TriggerClosed       Trigger = "conversation.closed"        // 会话关闭
)

// Triggers 是全部触发时机，显示文字是翻译文件中 automation.trigger.<触发> 的消息。
var Triggers = enum.New("automation.trigger", enum.Item(TriggerFirstMessage), enum.Item(TriggerClosed))

// Schema 让 OpenAPI 中的 Trigger 只接受上面的取值，并带有显示文字。
func (Trigger) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Triggers) }

// BuiltinKey 是内置自动化规则的固定标识；自定义规则为空字符串。
type BuiltinKey string

const (
	BuiltinReplyTime          BuiltinKey = "reply_time"           // 客户发来第一条消息时告知回复时间
	BuiltinCollectLeadContact BuiltinKey = "collect_lead_contact" // 线索发来第一条消息时请他留下联系方式
	BuiltinRequestRating      BuiltinKey = "request_rating"       // 会话关闭时请客户评价
)

// BuiltinKeys 是全部内置规则标识，显示文字是翻译文件中 automation.builtin.<标识> 的消息。
var BuiltinKeys = enum.New("automation.builtin", enum.Item(BuiltinReplyTime), enum.Item(BuiltinCollectLeadContact), enum.Item(BuiltinRequestRating))

// Schema 让 OpenAPI 中的 BuiltinKey 只接受上面的取值，并带有显示文字。
func (BuiltinKey) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, BuiltinKeys) }

// RunStatus 是一次自动化执行的状态。
type RunStatus string

const (
	RunExecuted RunStatus = "executed" // 执行成功
	RunFailed   RunStatus = "failed"   // 执行失败，可随队列重试
)

// Condition 是声明式的条件断言树：All（全部满足）、Any（满足其一）或单个字段断言（Field + Op + Value）。
// 空的 Condition 恒为真。
type Condition struct {
	All   []Condition `json:"all,omitempty"`
	Any   []Condition `json:"any,omitempty"`
	Field string      `json:"field,omitempty"`
	Op    string      `json:"op,omitempty"`
	Value any         `json:"value,omitempty"`
}

// Action 是规则匹配后要执行的一个动作。
type Action struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params,omitempty"`
}

// Rule 是一条自动化规则，属于一个工作区。内置规则带固定的 BuiltinKey，管理员只能开关；
// 后续的自定义规则 BuiltinKey 为空。
type Rule struct {
	bun.BaseModel `bun:"table:automation_rules"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID   string     `bun:"tenant_id,notnull"`
	BuiltinKey BuiltinKey `bun:"builtin_key,notnull"`
	Name       string     `bun:"name,notnull"`
	Trigger    Trigger    `bun:"trigger,notnull"`
	Conditions Condition  `bun:"conditions,type:jsonb,notnull"`
	Actions    []Action   `bun:"actions,type:jsonb,notnull"`
	Enabled    bool       `bun:"enabled,notnull"`
	Position   int        `bun:"position,notnull"`
}

// Run 是一条自动化规则针对某个去重范围的执行记录：按 (rule_id, dedup_key) 唯一，防止重复执行并保留审计。
type Run struct {
	bun.BaseModel `bun:"table:automation_runs"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID       string    `bun:"tenant_id,notnull"`
	RuleID         string    `bun:"rule_id,notnull"`
	ConversationID string    `bun:"conversation_id,notnull"`
	TriggerItemID  string    `bun:"trigger_item_id,notnull"`
	DedupKey       string    `bun:"dedup_key,notnull"`
	Status         RunStatus `bun:"status,notnull"`
	Error          string    `bun:"error,notnull"`
}

// RuleDTO 是接口中的自动化规则。
type RuleDTO struct {
	ID         string     `json:"id"`
	BuiltinKey BuiltinKey `json:"builtin_key,omitempty" doc:"内置规则的固定标识；自定义规则为空"`
	Name       string     `json:"name" doc:"规则名称；内置规则为空，前端按 builtin_key 显示"`
	Trigger    Trigger    `json:"trigger" doc:"触发时机"`
	Enabled    bool       `json:"enabled" doc:"是否启用"`
	Position   int        `json:"position" doc:"同一触发时机下的执行顺序，小的先执行"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// toDTO 把规则模型 r 转换为接口中的形式。
func toDTO(r Rule) RuleDTO {
	return RuleDTO{
		ID: r.ID, BuiltinKey: r.BuiltinKey, Name: r.Name,
		Trigger: r.Trigger, Enabled: r.Enabled, Position: r.Position, UpdatedAt: r.UpdatedAt,
	}
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// now 返回当前时间，精度与 PostgreSQL 的 timestamptz 一致。
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
