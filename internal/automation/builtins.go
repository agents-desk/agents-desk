package automation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"agents-desk/internal/contact"
)

// 动作类型常量。
const (
	ActionSendReplyTime      = "send_reply_time"      // 按工作时间告知客户回复时间或下次上班时间
	ActionRequestContactInfo = "request_contact_info" // 请线索留下邮箱
	ActionRequestRating      = "request_rating"       // 会话关闭后向客户发出评价邀请
	ActionNotifyTicketStatus = "notify_ticket_status" // 工单状态变化时通知关联服务型会话中的客户
)

// builtinDef 是一条内置自动化规则的声明，用来为工作区初始化规则。
type builtinDef struct {
	Key        BuiltinKey
	Trigger    Trigger
	Conditions Condition
	Actions    []Action
	Position   int
	Enabled    bool
}

// builtins 是第一版内置的三条自动化规则，全部默认开启，管理员可以在设置中逐条开关。
// 同一触发时机按 Position 从小到大执行：先告知回复时间，再请线索留联系方式。
var builtins = []builtinDef{
	{
		Key:      BuiltinReplyTime,
		Trigger:  TriggerFirstMessage,
		Actions:  []Action{{Type: ActionSendReplyTime}},
		Position: 10,
		Enabled:  true,
	},
	{
		Key:     BuiltinCollectLeadContact,
		Trigger: TriggerFirstMessage,
		Conditions: Condition{All: []Condition{
			{Field: "contact.kind", Op: "eq", Value: string(contact.Lead)},
			{Field: "contact.email", Op: "empty"},
		}},
		Actions:  []Action{{Type: ActionRequestContactInfo}},
		Position: 20,
		Enabled:  true,
	},
	{
		Key:      BuiltinRequestRating,
		Trigger:  TriggerClosed,
		Actions:  []Action{{Type: ActionRequestRating}},
		Position: 10,
		Enabled:  true,
	},
	{
		Key:      BuiltinTicketStatusNotify,
		Trigger:  TriggerTicketStatusChanged,
		Actions:  []Action{{Type: ActionNotifyTicketStatus}},
		Position: 10,
		Enabled:  true,
	},
}

// EnsureBuiltins 确保工作区 tenantID 中存在全部内置自动化规则；已经存在的保持管理员设置的开关不变。
// 可以在新建工作区的事务中调用，也可以在读取规则或处理事件前幂等调用。
func EnsureBuiltins(ctx context.Context, db bun.IDB, tenantID string) error {
	at := now()
	rows := make([]Rule, len(builtins))
	for i, b := range builtins {
		rows[i] = Rule{
			ID: newID(), TenantID: tenantID, BuiltinKey: b.Key, Trigger: b.Trigger,
			Conditions: b.Conditions, Actions: b.Actions, Enabled: b.Enabled, Position: b.Position,
		}
		rows[i].CreatedAt, rows[i].UpdatedAt = at, at
	}
	_, err := db.NewInsert().Model(&rows).
		On("CONFLICT (tenant_id, builtin_key) WHERE builtin_key <> '' DO NOTHING").Exec(ctx)
	return errors.Wrap(err, "ensure builtin automations")
}
