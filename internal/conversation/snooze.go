package conversation

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// SnoozeInput 是稍后处理的请求：到什么时间回到打开。
type SnoozeInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Until time.Time `json:"until" doc:"回到打开的时间，必须晚于现在；“明天”“下周一”这类选项由客户端按成员的时区算出"`
	}
}

// Snooze 稍后处理会话：暂时放下，到时间或对方回复时回到打开。
type Snooze struct {
	db   *bun.DB
	down Downstream
}

// NewSnooze 返回稍后处理的动作；down 是时间线有新内容时通知的下游。
func NewSnooze(db *bun.DB, down Downstream) *Snooze { return &Snooze{db: db, down: down} }

// Handle 由 sub 把他所在工作区中的会话 id 稍后处理到 until，时间线中追加 conversation.snoozed。已经在稍后处理时改为新的时间。
// until 不晚于现在时返回 ErrSnoozeInPast（字段 until）；会话已关闭时返回 ErrClosed。
func (a *Snooze) Handle(ctx context.Context, sub identity.Subject, id string, until time.Time) (*ConversationDTO, error) {
	until = until.UTC().Truncate(time.Microsecond)
	if !until.After(now()) {
		return nil, ErrSnoozeInPast.New(errorx.Field("until"))
	}
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(_ context.Context, _ bun.IDB, c *Conversation) ([]*Item, error) {
		return applySnooze(sub, c, until)
	})
}

// applySnooze 把会话 c 设为稍后处理到 until 并返回系统事件；时间不在将来返回 ErrSnoozeInPast，已关闭返回 ErrClosed。
func applySnooze(sub identity.Subject, c *Conversation, until time.Time) ([]*Item, error) {
	until = until.UTC().Truncate(time.Microsecond)
	if !until.After(now()) {
		return nil, ErrSnoozeInPast.New(errorx.Field("until"))
	}
	if c.Status == Closed {
		return nil, ErrClosed.New()
	}
	if c.Status == Snoozed && c.SnoozedUntil != nil && c.SnoozedUntil.Equal(until) {
		return nil, nil
	}
	c.Status, c.SnoozedUntil = Snoozed, &until
	return []*Item{event(sub, "conversation.snoozed", map[string]any{"until": until})}, nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/snooze。
func (a *Snooze) HTTP(ctx context.Context, in *SnoozeInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.Until)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
