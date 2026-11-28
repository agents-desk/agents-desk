// Package agent 是会话中的 AI 参与者：机器人（bots）、基于 tenon/ai/chat 的运行时服务与生成状态机（generations）。
//
// 生成是可变、短生命周期的状态：模型逐字产出的内容先写进 generations，只通过实时频道送给打开页面的人；
// reply 模式定稿时在同一个事务里追加一条不可变的时间线消息，draft 模式则保留为副驾驶草稿供成员编辑或采纳。
package agent

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrNotFound 是生成不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "agent.generation_not_found")
	// ErrFinished 是生成已经结束（完成、取消或失败），不能继续写入、取消或再次定稿。
	ErrFinished = errorx.Define(errorx.Conflict, "agent.generation_finished")
	// ErrForbidden 是不是系统主体，或者没有执行这个生成动作的权限。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "agent.forbidden")
	// ErrBotNotFound 是机器人不存在、已停用，或者属于别的工作区。
	ErrBotNotFound = errorx.Define(errorx.NotFound, "agent.bot_not_found")
	// ErrEmptyReply 是模型没有产出任何内容，不产生消息。
	ErrEmptyReply = errorx.Define(errorx.InvalidArgument, "agent.empty_reply")
	// ErrBotInUse 是机器人正被未关闭的会话、渠道自动接待配置或活跃生成占用，不能删除。
	ErrBotInUse = errorx.Define(errorx.Conflict, "agent.bot_in_use")
	// ErrApprovalNotPending 是工具调用审批不存在或已经处理过。
	ErrApprovalNotPending = errorx.Define(errorx.Conflict, "agent.approval_not_pending")
	// ErrAlreadyActive 是该会话已存在进行中的生成任务。
	ErrAlreadyActive = errorx.Define(errorx.Conflict, "agent.already_active")
)

// State 是生成的阶段。
type State string

const (
	Pending         State = "pending"          // 已创建，还没有产出
	Streaming       State = "streaming"        // 正在逐字产出
	WaitingApproval State = "waiting_approval" // 等待成员确认工具调用
	Completed       State = "completed"        // 已定稿为时间线中的一条消息（reply）或草稿（draft）
	Failed          State = "failed"           // 失败，没有产生消息
	Canceled        State = "canceled"         // 被取消，没有产生消息
)

// States 是生成的全部阶段，显示文字是翻译文件中 agent.generation_state.<阶段> 的消息。
var States = enum.New("agent.generation_state",
	enum.Item(Pending), enum.Item(Streaming), enum.Item(WaitingApproval),
	enum.Item(Completed), enum.Item(Failed), enum.Item(Canceled))

// Schema 让 OpenAPI 中的 State 只接受上面的取值，并带有显示文字。
func (State) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, States) }

// active 报告生成是否还没有结束：只有这些阶段能继续写入和取消。
func (s State) active() bool { return s == Pending || s == Streaming || s == WaitingApproval }

// Mode 是生成的投递模式：reply 直接写入时间线并发给客户，draft 作为副驾驶草稿等待成员审阅采纳。
type Mode string

const (
	ModeReply Mode = "reply" // 直接回复：定稿时写入时间线并推送客户
	ModeDraft Mode = "draft" // 副驾驶草稿：只保留在生成记录中供成员编辑或采纳
)

// Modes 是生成的全部模式，显示文字是翻译文件中 agent.generation_mode.<模式> 的消息。
var Modes = enum.New("agent.generation_mode", enum.Item(ModeReply), enum.Item(ModeDraft))

// Schema 让 OpenAPI 中的 Mode 只接受上面的取值，并带有显示文字。
func (Mode) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Modes) }

const (
	// BargeInNotify 表示生成中途收到新客户消息时继续当前回复并在完成后追加补充轮次。
	BargeInNotify = "notify"
	// BargeInCancelRestart 表示生成中途收到新客户消息时立即取消旧生成并以新上下文重启。
	BargeInCancelRestart = "cancel_restart"
)

// BotSettings 是 robots.settings 的结构化配置。
type BotSettings struct {
	SystemPrompt         string   `json:"system_prompt,omitempty" maxLength:"8000" doc:"机器人的核心系统提示词"`
	PromptVersion        string   `json:"prompt_version,omitempty" maxLength:"100" doc:"当前提示词版本标识"`
	Model                string   `json:"model,omitempty" maxLength:"200" doc:"可选覆写模型逻辑名；留空使用平台默认对话模型"`
	Temperature          *float64 `json:"temperature,omitempty" minimum:"0" maximum:"2" doc:"采样温度（0-2）"`
	AllowedTools         []string `json:"allowed_tools,omitempty" doc:"允许该机器人调用的工具白名单；留空启用默认安全工具集"`
	RequireApprovalTools []string `json:"require_approval_tools,omitempty" doc:"调用前必须经人工审批确认的工具列表"`
	BargeInPolicy        string   `json:"barge_in_policy,omitempty" doc:"客户插话策略：notify（默认）或 cancel_restart"`
	DebounceMS           int      `json:"debounce_ms,omitempty" minimum:"0" maximum:"15000" doc:"合并连续输入的消息防抖时间（毫秒）"`
	MaxToolIterations    int      `json:"max_tool_iterations,omitempty" minimum:"1" maximum:"20" doc:"单轮生成允许的最大工具调用步数"`
}

// ToolApproval 是等待或已完成的工具调用人工审批记录。
type ToolApproval struct {
	CallID     string     `json:"call_id"`
	Name       string     `json:"name"`
	Arguments  string     `json:"arguments,omitempty"`
	Status     string     `json:"status"` // pending、approved、rejected
	ReviewedBy string     `json:"reviewed_by,omitempty"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	Reason     string     `json:"reason,omitempty"`
}

// ToolCallRecord 是单次生成期间执行工具调用的审计摘要。
type ToolCallRecord struct {
	CallID        string `json:"call_id"`
	Name          string `json:"name"`
	DurationMS    int64  `json:"duration_ms"`
	ResultSummary string `json:"result_summary,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Bot 是一个机器人（AI 助手或外部 agent），属于一个工作区，可以作为一个参与者或主负责人加入会话。
type Bot struct {
	bun.BaseModel `bun:"table:bots"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID string         `bun:"tenant_id,notnull"`
	Name     string         `bun:"name,notnull"`
	Enabled  bool           `bun:"enabled,notnull"`
	Settings map[string]any `bun:"settings,type:jsonb,notnull"` // 结构见 BotSettings
}

// Config 解析机器人的结构化设置，并补齐默认值。
func (b *Bot) Config() BotSettings {
	var cfg BotSettings
	if len(b.Settings) > 0 {
		raw, _ := json.Marshal(b.Settings)
		_ = json.Unmarshal(raw, &cfg)
	}
	if cfg.BargeInPolicy != BargeInCancelRestart {
		cfg.BargeInPolicy = BargeInNotify
	}
	if cfg.MaxToolIterations <= 0 {
		cfg.MaxToolIterations = 8
	}
	if strings.TrimSpace(cfg.PromptVersion) == "" {
		cfg.PromptVersion = "v1"
	}
	return cfg
}

// Generation 是一次 AI 回复的生成：可变、短生命周期，结束时按模式定稿为时间线消息或副驾驶草稿。
type Generation struct {
	bun.BaseModel `bun:"table:generations"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID         string           `bun:"tenant_id,notnull"`
	ConversationID   string           `bun:"conversation_id,notnull"`
	ActorID          string           `bun:"actor_id,notnull"`        // 产出回复的机器人
	TriggerItemID    string           `bun:"trigger_item_id,notnull"` // 触发这次生成的时间线项
	TriggerSeq       int64            `bun:"trigger_seq,notnull"`     // 触发这次生成的消息时间线序号
	Mode             Mode             `bun:"mode,notnull"`            // 生成模式：reply 或 draft
	State            State            `bun:"state,notnull"`
	RunID            string           `bun:"run_id,notnull"` // ai/chat.Run 标识
	Parts            map[string]any   `bun:"parts,type:jsonb,notnull"`
	Checkpoint       int64            `bun:"checkpoint_seq,notnull"` // 产出片段的序号，客户端用它续上气泡
	ItemID           *string          `bun:"item_id"`                // 定稿后的时间线项；未定稿或 draft 模式时为空
	Error            string           `bun:"error,notnull"`
	CancelReason     string           `bun:"cancel_reason,notnull"`
	PendingApprovals []ToolApproval   `bun:"pending_approvals,type:jsonb,notnull"`
	ToolCalls        []ToolCallRecord `bun:"tool_calls,type:jsonb,notnull"`
	Citations        []Citation       `bun:"citations,type:jsonb,notnull"`
	PromptVersion    string           `bun:"prompt_version,notnull"`
	Usage            map[string]any   `bun:"usage,type:jsonb,notnull"`
	ExpiresAt        time.Time        `bun:"expires_at,notnull"` // 超过这个时间仍未结束的生成视为失败，由定时任务清理
}

// AgentTurn 保存 AI 运行过程中的中间轮次消息（带工具调用的 assistant 消息与 tool 结果消息），
// 供 tenon/ai/chat 的 HistoryStore 在断点审批恢复或续写上下文时按顺序重放。
type AgentTurn struct {
	bun.BaseModel `bun:"table:agent_turns"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID       string          `bun:"tenant_id,notnull"`
	ConversationID string          `bun:"conversation_id,notnull"`
	GenerationID   *string         `bun:"generation_id"`
	RunID          string          `bun:"run_id,notnull"`
	AfterSeq       int64           `bun:"after_seq,notnull"`
	TurnOrder      int             `bun:"turn_order,notnull"`
	Role           string          `bun:"role,notnull"`
	Message        json.RawMessage `bun:"message,type:jsonb,notnull"`
}

// text 返回已经产出的文字。
func (g *Generation) text() string {
	s, _ := g.Parts["text"].(string)
	return s
}
