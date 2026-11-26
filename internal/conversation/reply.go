package conversation

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// Translation 是成员确认过的发给对方的译文。
type Translation struct {
	Language string `json:"language" minLength:"2" maxLength:"35" doc:"译文的语言（BCP 47），例如 hi-Latn"`
	Text     string `json:"text" minLength:"1" maxLength:"20000" doc:"发给对方的译文"`
}

// ReplyBody 是回复的请求体：成员写的文字、可选的媒体附件列表，以及可选的、他确认过的译文和随回复原子执行的会话操作。
// 文字和附件不能同时为空；带译文时发给对方的是译文，时间线中同时保存原文。
type ReplyBody struct {
	Text        string            `json:"text,omitempty" maxLength:"10000" doc:"成员写的文字"`
	Attachments []AttachmentInput `json:"attachments,omitempty" maxItems:"10" doc:"通过 /files 上传的媒体附件列表（可附带封面图与音视频元数据）"`
	Translation *Translation      `json:"translation,omitempty" doc:"发给对方的译文，由翻译的预览给出、成员确认；不带时按原文发出"`
	Actions     *Actions          `json:"actions,omitempty" doc:"随回复在同一个事务中原子执行的会话操作（如指派、改优先级、加标签、改属性、关闭或稍后处理）"`
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
	if err := requireMessageContent(body.Text, len(body.Attachments)); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if c.Kind != Service {
			return ErrWrongKind.New()
		}
		confirmed, err := confirmAttachmentInputs(ctx, tx, a.down.Media.Files, sub, body.Attachments)
		if err != nil {
			return err
		}
		at := now()
		actor := actorOf(sub)
		content := Content{Text: body.Text}
		if body.Translation != nil && strings.TrimSpace(body.Text) != "" {
			content = Content{Text: body.Translation.Text, Original: body.Text, Language: body.Translation.Language}
		}
		deliveryChannelID := c.DeliveryChannelID()
		item = Item{Kind: Message, ActorType: actor, ActorID: sub.ID, Visibility: Everyone, Content: content, ChannelID: deliveryChannelID}
		items := make([]*Item, 0, 3)
		// 回复已关闭或稍后处理的会话时自动恢复打开，保证轮次与状态机自洽
		switch c.Status {
		case Closed:
			items = append(items, event(sub, "conversation.reopened", map[string]any{"from": string(Closed)}))
			c.Status, c.SnoozedUntil, c.OpenedAt, c.FirstReplyAt = Open, nil, at, nil
			if err := openRound(ctx, tx, c, at); err != nil {
				return err
			}
		case Snoozed:
			items = append(items, event(sub, "conversation.unsnoozed", map[string]any{"from": string(Snoozed)}))
			c.Status, c.SnoozedUntil = Open, nil
		}
		// 回复即领取：成员回复没有负责人的会话时，会话归他，团队收件箱不变；
		// 若随回复显式携带指派操作，则跳过自动领取，直接按附带指派执行，避免产生两条指派事件。
		explicitAssign := body.Actions != nil && body.Actions.HasAssignment()
		if actor == MemberActor && c.AssigneeType == "" && !explicitAssign {
			c.AssigneeType, c.AssigneeID = MemberActor, sub.ID
			items = append(items, assignedEvent(sub, c, c.TeamID, "", "", ReasonReply))
		}
		items = append(items, &item)
		c.WaitingSince, c.LastMessageAt = nil, at
		if c.FirstReplyAt == nil {
			c.FirstReplyAt = &at
			if err := recordFirstReply(ctx, tx, c, actor, sub.ID, at); err != nil {
				return err
			}
		}
		if err := join(ctx, tx, c.ID, actor, sub.ID); err != nil {
			return err
		}
		if body.Actions != nil && body.Actions.HasChanges() {
			ev, err := applyActions(ctx, tx, sub, c, *body.Actions)
			if err != nil {
				return err
			}
			items = append(items, ev...)
		}
		if err := commit(ctx, tx, a.down, c, items...); err != nil {
			return err
		}
		if err := insertAttachments(ctx, tx, c.TenantID, item.ID, confirmed); err != nil {
			return err
		}
		if deliveryChannelID == nil {
			return nil
		}
		_, err = tx.NewInsert().Model(&Delivery{ItemID: item.ID, Status: Sending, UpdatedAt: at}).Exec(ctx)
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
