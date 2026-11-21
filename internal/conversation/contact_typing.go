package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// TypingByContact 是客户在聊天窗口中正在输入：推送到会话的频道，打开着这个会话的成员看到“正在输入”。
type TypingByContact struct {
	db   *bun.DB
	down Downstream
}

// NewTypingByContact 返回客户正在输入的动作；down 的 Hub 推送到会话的频道，为 nil 时什么也不做。
func NewTypingByContact(db *bun.DB, down Downstream) *TypingByContact {
	return &TypingByContact{db: db, down: down}
}

// Handle 推送联系人 sub 在渠道 channelID 的会话 id 中正在输入。最多送达一次，不保存；聊天窗口在客户输入时每隔几秒调用一次。
// sub 不是联系人时返回 ErrForbidden；会话不存在、属于别人或别的渠道时返回 ErrNotFound。
func (a *TypingByContact) Handle(ctx context.Context, sub identity.Subject, channelID, id string) error {
	c, err := findForContact(ctx, a.db, sub, channelID, id, false)
	if err != nil || a.down.Hub == nil {
		return err
	}
	ev := Typing{Type: "typing", Actor: Actor{Type: ContactActor, ID: sub.ID}}
	return errors.Wrap(a.down.Hub.PublishJSON(ctx, Channel(c.TenantID, c.ID), ev), "push contact typing")
}
