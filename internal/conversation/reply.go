package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// TextBody 是内部备注等只有文字的请求体。
type TextBody struct {
	Text string `json:"text" minLength:"1" maxLength:"10000"`
}

// Translation 是成员确认过的发给对方的译文。
type Translation struct {
	Language string `json:"language" minLength:"2" maxLength:"35" doc:"译文的语言（BCP 47），例如 hi-Latn"`
	Text     string `json:"text" minLength:"1" maxLength:"20000" doc:"发给对方的译文"`
}

// ReplyBody 是回复的请求体：成员写的文字，以及可选的、他确认过的译文。带译文时发给对方的是译文，时间线中同时保存原文。
type ReplyBody struct {
	Text        string       `json:"text" minLength:"1" maxLength:"10000" doc:"成员写的文字"`
	Translation *Translation `json:"translation,omitempty" doc:"发给对方的译文，由翻译的预览给出、成员确认；不带时按原文发出"`
}

// ReplyInput 是回复会话的 HTTP 请求。
type ReplyInput struct {
	ID   string `path:"id" format:"uuid"`
	Body ReplyBody
}

// Reply 是成员回复会话：消息发给对方。
type Reply struct {
	db   *bun.DB
	down Downstream
}

// NewReply 返回回复会话的动作；down 是时间线有新内容时通知的下游，其中包括把消息发到外部渠道。
func NewReply(db *bun.DB, down Downstream) *Reply { return &Reply{db: db, down: down} }

// Handle 由 sub 回复 sub 所在工作区中的会话 id：追加一条发给对方的消息（带译文时发出译文，时间线中同时保存原文），记为发送中；会话不再等待回复，
// 第一次回复时记下时间；sub 成为会话的参与者。会话没有负责人时由 sub 领取（回复即领取），时间线中先追加领取的系统事件。
// 会话不存在或 sub 看不到时返回 ErrNotFound，没有权限时返回 ErrForbidden。
func (a *Reply) Handle(ctx context.Context, sub identity.Subject, id string, body ReplyBody) (*ItemDTO, error) {
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
		content := Content{Text: body.Text}
		if body.Translation != nil {
			content = Content{Text: body.Translation.Text, Original: body.Text, Language: body.Translation.Language}
		}
		item = Item{Kind: Message, ActorType: actor, ActorID: sub.ID, Visibility: Everyone, Content: content, ChannelID: c.ChannelID}
		items := []*Item{&item}
		// 回复即领取：成员回复没有负责人的会话时，会话归他，团队收件箱不变
		if actor == MemberActor && c.AssigneeType == "" {
			c.AssigneeType, c.AssigneeID = MemberActor, sub.ID
			items = append([]*Item{assignedEvent(sub, c, c.TeamID, "", "", ReasonReply)}, items...)
		}
		at := now()
		c.WaitingSince, c.LastMessageAt = nil, at
		if c.FirstReplyAt == nil {
			c.FirstReplyAt = &at
		}
		if err := join(ctx, tx, c.ID, actor, sub.ID); err != nil {
			return err
		}
		if err := commit(ctx, tx, a.down, c, items...); err != nil {
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
func (a *Reply) HTTP(ctx context.Context, in *ReplyInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body)
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
