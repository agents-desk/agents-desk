package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// SendSystemMessage 由系统（例如自动化）在会话中发一条对所有人可见的消息：经连接器发给对方，
// 但不影响会话的等待回复状态（WaitingSince）、首次回复时间（FirstReplyAt）、负责人和最近活动时间。
type SendSystemMessage struct {
	db   *bun.DB
	down Downstream
}

// NewSendSystemMessage 返回系统发消息的动作；down 是时间线有新内容时通知的下游，其中包括把消息发到外部渠道。
func NewSendSystemMessage(db *bun.DB, down Downstream) *SendSystemMessage {
	return &SendSystemMessage{db: db, down: down}
}

// Handle 以系统主体 sub 在工作区中向会话 id 追加一条系统消息：text 是纯文本回退内容（供不支持卡片的外部渠道发出），
// prompt 是可选的结构化提示或交互卡片（供聊天插件和收件箱按语言渲染）。
// 只有系统主体可以调用，否则返回 ErrForbidden；会话不存在时返回 ErrNotFound。
func (a *SendSystemMessage) Handle(ctx context.Context, sub identity.Subject, id, text string, prompt *Prompt) (*ItemDTO, error) {
	if err := authorize(sub, ""); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		item = Item{
			Kind: Message, ActorType: SystemActor, ActorID: sub.ID, Visibility: Everyone,
			Content: Content{Text: text, Prompt: prompt}, ChannelID: c.ChannelID,
		}
		if err := commit(ctx, tx, a.down, c, &item); err != nil {
			return err
		}
		if c.ChannelID == nil {
			return nil
		}
		_, err = tx.NewInsert().Model(&Delivery{ItemID: item.ID, Status: Sending, UpdatedAt: item.CreatedAt}).Exec(ctx)
		return errors.Wrap(err, "insert delivery")
	})
	if err != nil {
		return nil, err
	}
	dtos, err := toItemDTOs(ctx, a.db, a.down.Media, []Item{item})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}
