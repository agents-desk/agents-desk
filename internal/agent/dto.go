package agent

import (
	"time"

	"agents-desk/internal/conversation"
)

// GenerationDTO 是接口中的一次生成：进行中的生成连同已产出的文字一起返回，客户端据此恢复气泡。
type GenerationDTO struct {
	ID             string             `json:"id"`
	ConversationID string             `json:"conversation_id"`
	Actor          conversation.Actor `json:"actor" doc:"产出回复的机器人"`
	State          State              `json:"state"`
	Text           string             `json:"text" doc:"已产出的文字"`
	Checkpoint     int64              `json:"checkpoint" doc:"产出片段的序号，客户端用它续上气泡"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

// dto 返回生成 g 的接口形式；actor 是机器人的显示资料。
func (g *Generation) dto(actor conversation.Actor) GenerationDTO {
	return GenerationDTO{
		ID:             g.ID,
		ConversationID: g.ConversationID,
		Actor:          actor,
		State:          g.State,
		Text:           g.text(),
		Checkpoint:     g.Checkpoint,
		CreatedAt:      g.CreatedAt,
		UpdatedAt:      g.UpdatedAt,
	}
}

// Frame 是生成频道中的事件：delta 是一段新文字，done 表示已定稿（带时间线项和序号），failed、canceled 表示没有产生消息。
type Frame struct {
	Type           string `json:"type"`
	GenerationID   string `json:"generation_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	ItemID         string `json:"item_id,omitempty"`
	Seq            int64  `json:"seq,omitempty"`
	Delta          string `json:"delta,omitempty"`
	Checkpoint     int64  `json:"checkpoint,omitempty"`
	State          State  `json:"state,omitempty"`
}
