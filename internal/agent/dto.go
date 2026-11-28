package agent

import (
	"time"

	"agents-desk/internal/conversation"
)

// BotDTO 是接口返回的机器人资料与配置。
type BotDTO struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Enabled   bool        `json:"enabled"`
	Settings  BotSettings `json:"settings"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// dto 返回机器人 b 的接口形式。
func (b *Bot) dto() BotDTO {
	return BotDTO{
		ID:        b.ID,
		Name:      b.Name,
		Enabled:   b.Enabled,
		Settings:  b.Config(),
		CreatedAt: b.CreatedAt,
		UpdatedAt: b.UpdatedAt,
	}
}

// GenerationDTO 是接口中的一次生成：进行中的生成（及最新已完成草稿）连同状态、审批项与引用一并返回。
type GenerationDTO struct {
	ID               string             `json:"id"`
	ConversationID   string             `json:"conversation_id"`
	Actor            conversation.Actor `json:"actor" doc:"产出回复的机器人"`
	Mode             Mode               `json:"mode"`
	State            State              `json:"state"`
	RunID            string             `json:"run_id,omitempty"`
	TriggerSeq       int64              `json:"trigger_seq"`
	Text             string             `json:"text" doc:"已产出的文字"`
	Checkpoint       int64              `json:"checkpoint" doc:"产出片段的序号，客户端用它续上气泡"`
	CancelReason     string             `json:"cancel_reason,omitempty"`
	PendingApprovals []ToolApproval     `json:"pending_approvals" nullable:"false"`
	ToolCalls        []ToolCallRecord   `json:"tool_calls" nullable:"false"`
	Citations        []Citation         `json:"citations" nullable:"false"`
	PromptVersion    string             `json:"prompt_version,omitempty"`
	Error            string             `json:"error,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

// dto 返回生成 g 的接口形式；actor 是机器人的显示资料。
func (g *Generation) dto(actor conversation.Actor) GenerationDTO {
	approvals := g.PendingApprovals
	if approvals == nil {
		approvals = []ToolApproval{}
	}
	calls := g.ToolCalls
	if calls == nil {
		calls = []ToolCallRecord{}
	}
	cites := g.Citations
	if cites == nil {
		cites = []Citation{}
	}
	mode := g.Mode
	if mode == "" {
		mode = ModeReply
	}
	return GenerationDTO{
		ID:               g.ID,
		ConversationID:   g.ConversationID,
		Actor:            actor,
		Mode:             mode,
		State:            g.State,
		RunID:            g.RunID,
		TriggerSeq:       g.TriggerSeq,
		Text:             g.text(),
		Checkpoint:       g.Checkpoint,
		CancelReason:     g.CancelReason,
		PendingApprovals: approvals,
		ToolCalls:        calls,
		Citations:        cites,
		PromptVersion:    g.PromptVersion,
		Error:            g.Error,
		CreatedAt:        g.CreatedAt,
		UpdatedAt:        g.UpdatedAt,
	}
}

// Frame 是生成频道中的实时事件帧。
type Frame struct {
	Type             string         `json:"type"` // delta、done、failed、canceled、approval_required
	GenerationID     string         `json:"generation_id"`
	ConversationID   string         `json:"conversation_id,omitempty"`
	Mode             Mode           `json:"mode,omitempty"`
	ItemID           string         `json:"item_id,omitempty"`
	Seq              int64          `json:"seq,omitempty"`
	Delta            string         `json:"delta,omitempty"`
	Checkpoint       int64          `json:"checkpoint,omitempty"`
	State            State          `json:"state,omitempty"`
	PendingApprovals []ToolApproval `json:"pending_approvals,omitempty"`
	Citations        []Citation     `json:"citations,omitempty"`
	CancelReason     string         `json:"cancel_reason,omitempty"`
	Error            string         `json:"error,omitempty"`
}
