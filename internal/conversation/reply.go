package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// TextBody 是回复和内部备注的请求体。
type TextBody struct {
	Text string `json:"text" minLength:"1" maxLength:"10000"`
}

// TextInput 是向会话写入文字的 HTTP 请求。
type TextInput struct {
	ID   string `path:"id" format:"uuid"`
	Body TextBody
}

// Reply 是成员回复会话：消息发给对方。
type Reply struct {
	db   *bun.DB
	down Downstream
}

// NewReply 返回回复会话的动作；down 是时间线有新内容时通知的下游，其中包括把消息发到外部渠道。
func NewReply(db *bun.DB, down Downstream) *Reply { return &Reply{db: db, down: down} }

// Handle 由 sub 回复 sub 所在工作区中的会话 id：追加一条发给对方的消息，记为发送中；会话不再等待回复，
// 第一次回复时记下时间；sub 成为会话的参与者。会话不存在或 sub 看不到时返回 ErrNotFound，没有权限时返回 ErrForbidden。
func (a *Reply) Handle(ctx context.Context, sub identity.Subject, id, text string) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		actor := actorOf(sub)
		item = Item{Kind: Message, ActorType: actor, ActorID: sub.ID, Visibility: Everyone, Content: Content{Text: text}, ChannelID: c.ChannelID}
		at := now()
		c.WaitingSince, c.LastMessageAt = nil, at
		if c.FirstReplyAt == nil {
			c.FirstReplyAt = &at
		}
		if err := join(ctx, tx, c.ID, actor, sub.ID); err != nil {
			return err
		}
		if err := commit(ctx, tx, a.down, c, &item); err != nil {
			return err
		}
		if c.ChannelID == nil {
			return nil
		}
		_, err = tx.NewInsert().Model(&Delivery{ItemID: item.ID, Status: Sending, UpdatedAt: at}).Exec(ctx)
		return errors.Wrap(err, "insert delivery")
	})
	if err != nil {
		return nil, err
	}
	dtos, err := toItemDTOs(ctx, a.db, []Item{item})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/replies。
func (a *Reply) HTTP(ctx context.Context, in *TextInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body.Text)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}

// actorOf 返回主体 sub 在时间线中的发起者类型：成员是 member，其他主体（命令、种子数据）是 system。
func actorOf(sub identity.Subject) ActorType {
	if sub.Type == identity.System {
		return SystemActor
	}
	return MemberActor
}
