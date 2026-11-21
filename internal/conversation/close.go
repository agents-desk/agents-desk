package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// Close 关闭会话：处理完毕。对方再回复时会话重新打开。
type Close struct {
	db   *bun.DB
	down Downstream
}

// NewClose 返回关闭会话的动作；down 是时间线有新内容时通知的下游。
func NewClose(db *bun.DB, down Downstream) *Close { return &Close{db: db, down: down} }

// Handle 由 sub 关闭他所在工作区中的会话 id：记下关闭时间，清空稍后处理的时间，时间线中追加 conversation.closed。
// 已经关闭时什么也不做。团队收件箱和负责人保留，报表据此统计是谁处理的。
func (a *Close) Handle(ctx context.Context, sub identity.Subject, id string) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		if c.Status == Closed {
			return nil, nil
		}
		from := c.Status
		c.Status, c.ClosedAt, c.SnoozedUntil = Closed, timePtr(now()), nil
		return []*Item{event(sub, "conversation.closed", map[string]any{"from": string(from)})}, nil
	})
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/close。
func (a *Close) HTTP(ctx context.Context, in *IDInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
