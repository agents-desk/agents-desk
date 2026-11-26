package conversation

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
)

// Translation 是成员确认过的发给对方的译文。
type Translation struct {
	Language string `json:"language" minLength:"2" maxLength:"35" doc:"译文的语言（BCP 47），例如 hi-Latn"`
	Text     string `json:"text" minLength:"1" maxLength:"20000" doc:"发给对方的译文"`
}

// AutoTranslateRequest 指示服务端在后台投递前将成员原文异步翻译为目标语言，避免阻塞发送交互。
type AutoTranslateRequest struct {
	Language string `json:"language,omitempty" maxLength:"35" doc:"目标语言（BCP 47）；留空时按会话对方语言自动推断"`
	Tone     string `json:"tone,omitempty" maxLength:"20" doc:"可选语气覆盖（friendly、neutral 或 formal）；留空使用工作区默认语气"`
}

// pendingTranslationDerived 生成写入 timeline_items.derived 的异步待翻译标记。
func pendingTranslationDerived(req *AutoTranslateRequest) map[string]any {
	if req == nil {
		return nil
	}
	return map[string]any{
		"pending_translation": map[string]any{
			"language": strings.TrimSpace(req.Language),
			"tone":     strings.TrimSpace(req.Tone),
		},
	}
}

// ReplyBody 是回复的请求体：成员写的文字、可选的媒体附件列表、可选的发送渠道，以及可选的译文、后台自动翻译指示和随回复原子执行的会话操作。
// 文字和附件不能同时为空；带译文时发给对方的是译文，带 auto_translate 时先快速落库并由投递任务在后台翻译后发出，时间线中均保存原文。
type ReplyBody struct {
	Text          string                `json:"text,omitempty" maxLength:"10000" doc:"成员写的文字"`
	Attachments   []AttachmentInput     `json:"attachments,omitempty" maxItems:"10" doc:"通过 /files 上传的媒体附件列表（可附带封面图与音视频元数据）"`
	ChannelID     string                `json:"channel_id,omitempty" format:"uuid" doc:"发往的渠道 ID；为空时发往会话当前的默认回复渠道"`
	Translation   *Translation          `json:"translation,omitempty" doc:"发给对方的译文，由翻译的预览给出、成员确认；不带时按原文或 auto_translate 发出"`
	AutoTranslate *AutoTranslateRequest `json:"auto_translate,omitempty" doc:"后台自动翻译参数：不携带 translation 时，先立刻落库并由投递任务异步翻译后发给对方"`
	Actions       *Actions              `json:"actions,omitempty" doc:"随回复在同一个事务中原子执行的会话操作（如指派、改优先级、加标签、改属性、关闭或稍后处理）"`
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
		targetChannelID, err := resolveReplyChannel(ctx, tx, c, body.ChannelID)
		if err != nil {
			return err
		}
		if body.ChannelID != "" && targetChannelID != nil {
			chID := *targetChannelID
			c.ReplyChannelID = &chID
		}
		confirmed, err := confirmAttachmentInputs(ctx, tx, a.down.Media.Files, sub, body.Attachments)
		if err != nil {
			return err
		}
		at := now()
		actor := actorOf(sub)
		content := Content{Text: body.Text}
		var derived map[string]any
		if body.Translation != nil && strings.TrimSpace(body.Text) != "" {
			content = Content{Text: body.Translation.Text, Original: body.Text, Language: body.Translation.Language}
		} else if body.AutoTranslate != nil && strings.TrimSpace(body.Text) != "" {
			derived = pendingTranslationDerived(body.AutoTranslate)
		}
		item = Item{Kind: Message, ActorType: actor, ActorID: sub.ID, Visibility: Everyone, Content: content, Derived: derived, ChannelID: targetChannelID}
		items := make([]*Item, 0, 3)
		// 回复已关闭或稍后处理的会话时自动恢复打开，保证轮次与状态机自洽；若联系人已有其他活跃会话则拒绝重开
		switch c.Status {
		case Closed:
			if err := ensureNoOtherActive(ctx, tx, c); err != nil {
				return err
			}
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
		if targetChannelID == nil {
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

// resolveReplyChannel 解析回复要发往的渠道 ID：
//   - requested 为空时返回会话当前默认出站渠道（c.DeliveryChannelID()）；
//   - requested 非空时校验该渠道属于本工作区且处于启用状态，并且当会话关联联系人时该联系人拥有该渠道的身份，否则返回 ErrChannelUnavailable。
func resolveReplyChannel(ctx context.Context, tx bun.IDB, c *Conversation, requested string) (*string, error) {
	if requested == "" {
		return c.DeliveryChannelID(), nil
	}
	ch, err := channel.Find(ctx, tx, c.TenantID, requested)
	if errors.Is(err, channel.ErrNotFound) || (err == nil && !ch.Enabled) {
		return nil, ErrChannelUnavailable.New()
	}
	if err != nil {
		return nil, err
	}
	if c.ContactID != nil {
		exists, err := tx.NewSelect().Table("contact_identities").
			Where("tenant_id = ? AND channel_id = ? AND contact_id = ?", c.TenantID, ch.ID, *c.ContactID).Exists(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "check contact channel identity")
		}
		if !exists {
			return nil, ErrChannelUnavailable.New()
		}
	}
	id := ch.ID
	return &id, nil
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
