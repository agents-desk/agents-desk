package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// PriorityInput 是修改优先级的请求。
type PriorityInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Priority Priority `json:"priority"`
	}
}

// SetPriority 修改会话的优先级。
type SetPriority struct {
	db   *bun.DB
	down Downstream
}

// NewSetPriority 返回修改优先级的动作；down 是时间线有新内容时通知的下游。
func NewSetPriority(db *bun.DB, down Downstream) *SetPriority {
	return &SetPriority{db: db, down: down}
}

// Handle 由 sub 把他所在工作区中的会话 id 的优先级改为 p，时间线中追加 conversation.priority_changed；没有变化时什么也不做。
func (a *SetPriority) Handle(ctx context.Context, sub identity.Subject, id string, p Priority) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(_ context.Context, _ bun.IDB, c *Conversation) (*Item, error) {
		if c.Priority == p {
			return nil, nil
		}
		from := c.Priority
		c.Priority = p
		return event(sub, "conversation.priority_changed", map[string]any{"from": string(from), "to": string(p)}), nil
	})
}

// HTTP 处理 PUT /t/{slug}/api/conversations/{id}/priority。
func (a *SetPriority) HTTP(ctx context.Context, in *PriorityInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.Priority)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
