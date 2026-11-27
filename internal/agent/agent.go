// Package agent 是会话中的 AI 参与者：机器人（bots）与它们的生成（generations）。
//
// 生成是可变、短生命周期的状态：模型逐字产出的内容先写进 generations，只通过实时频道送给打开页面的人；
// 定稿时才在同一个事务里追加一条不可变的时间线消息。时间线因此保持只追加，搜索索引、译文、推送序号、
// 导出和读游标都不需要理解“半条消息”。
package agent

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
)

// State 是生成的阶段。
type State string

const (
	Pending         State = "pending"          // 已创建，还没有产出
	Streaming       State = "streaming"        // 正在逐字产出
	WaitingApproval State = "waiting_approval" // 等待成员确认工具调用
	Completed       State = "completed"        // 已定稿为时间线中的一条消息
	Failed          State = "failed"           // 失败，没有产生消息
	Canceled        State = "canceled"         // 被成员取消，没有产生消息
)

// States 是生成的全部阶段，显示文字是翻译文件中 agent.generation_state.<阶段> 的消息。
var States = enum.New("agent.generation_state",
	enum.Item(Pending), enum.Item(Streaming), enum.Item(WaitingApproval),
	enum.Item(Completed), enum.Item(Failed), enum.Item(Canceled))

// Schema 让 OpenAPI 中的 State 只接受上面的取值，并带有显示文字。
func (State) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, States) }

// active 报告生成是否还没有结束：只有这些阶段能继续写入和取消。
func (s State) active() bool { return s == Pending || s == Streaming || s == WaitingApproval }

// Bot 是一个机器人（AI 助手或外部 agent），属于一个工作区，可以作为一个参与者加入会话。
type Bot struct {
	bun.BaseModel `bun:"table:bots"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID string         `bun:"tenant_id,notnull"`
	Name     string         `bun:"name,notnull"`
	Enabled  bool           `bun:"enabled,notnull"`
	Settings map[string]any `bun:"settings,type:jsonb,notnull"` // 模型、系统提示词等配置，内容由具体用途决定
}

// Generation 是一次 AI 回复的生成：可变、短生命周期，结束时定稿为时间线中的一条消息。
type Generation struct {
	bun.BaseModel `bun:"table:generations"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID       string         `bun:"tenant_id,notnull"`
	ConversationID string         `bun:"conversation_id,notnull"`
	ActorID        string         `bun:"actor_id,notnull"`        // 产出回复的机器人
	TriggerItemID  string         `bun:"trigger_item_id,notnull"` // 触发这次生成的时间线项
	State          State          `bun:"state,notnull"`
	Parts          map[string]any `bun:"parts,type:jsonb,notnull"` // 已产出的内容，例如 text
	Checkpoint     int64          `bun:"checkpoint_seq,notnull"`   // 产出片段的序号，客户端用它续上气泡
	ItemID         *string        `bun:"item_id"`                  // 定稿后的时间线项；未定稿时为空
	Error          string         `bun:"error,notnull"`
	Usage          map[string]any `bun:"usage,type:jsonb,notnull"`
	ExpiresAt      time.Time      `bun:"expires_at,notnull"` // 超过这个时间仍未结束的生成视为失败，由定时任务清理
}

// text 返回已经产出的文字。
func (g *Generation) text() string {
	s, _ := g.Parts["text"].(string)
	return s
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// now 返回当前时间，精度与 PostgreSQL 的 timestamptz 一致。
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
