package conversation

import (
	"context"
	"fmt"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/member"
)

// SendChatMessageBody 是发送聊天消息的请求体：文字、@ 提及的成员、外部群 @ 提及、引用消息、媒体附件、可选内部备注标记、可选译文与后台自动翻译参数（文字与附件不能同时为空）。
type SendChatMessageBody struct {
	Text             string                    `json:"text,omitempty" maxLength:"10000" doc:"消息正文"`
	Mentions         []string                  `json:"mentions,omitempty" maxItems:"20" uniqueItems:"true" doc:"@ 到的工作区成员 ID（仅内部聊天或内部备注使用）"`
	ExternalMentions []channel.ExternalMention `json:"external_mentions,omitempty" maxItems:"20" doc:"外部群对外回复时要 @ 的发言人列表"`
	ReplyToItemID    string                    `json:"reply_to_item_id,omitempty" format:"uuid" doc:"可选：引用回复同聊天内的指定消息 ID"`
	CorrectsItemID   string                    `json:"corrects_item_id,omitempty" format:"uuid" doc:"可选：作为更正发出的原消息 ID"`
	Attachments      []AttachmentInput         `json:"attachments,omitempty" maxItems:"10" doc:"通过 /files 上传的媒体附件列表"`
	Note             bool                      `json:"note,omitempty" doc:"仅商务外部聊天可用：为 true 时写为仅成员可见的内部备注，不发往外部渠道"`
	Translation      *Translation              `json:"translation,omitempty" doc:"发送译文：外部商务聊天对外回复时替换发出并保留原文；内部聊天与内部备注时写入派生译文供该语言成员查看"`
	AutoTranslate    *AutoTranslateRequest     `json:"auto_translate,omitempty" doc:"仅外部商务聊天对外回复可用：不携带 translation 时，先立刻落库并由投递任务异步翻译后发往外部渠道"`
}

// SendChatMessageInput 是发送聊天消息的 HTTP 请求。
type SendChatMessageInput struct {
	ID   string `path:"id" format:"uuid"`
	Body SendChatMessageBody
}

// SendChatMessage 在聊天（内部单聊/群聊或外部商务沟通）中发送消息或内部备注，
// 并经由 Downstream 自动触发外部渠道投递（外部回复）、跨语言翻译、搜索索引与实时推送。
type SendChatMessage struct {
	db   *bun.DB
	down Downstream
}

// NewSendChatMessage 返回发送聊天消息的动作。
func NewSendChatMessage(db *bun.DB, down Downstream) *SendChatMessage {
	return &SendChatMessage{db: db, down: down}
}

// Handle 由参与者 sub 在聊天 id 中发送一条消息（或外部商务聊天中的内部备注）；
// 非参与者或非聊天型会话返回 ErrNotFound，空消息返回 ErrEmptyMessage，外部商务渠道已停用或不可用时返回 channel.ErrDisabled 或 ErrChannelUnavailable。
func (a *SendChatMessage) Handle(ctx context.Context, sub identity.Subject, id string, body SendChatMessageBody) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if err := requireMessageContent(body.Text, len(body.Attachments)); err != nil {
		return nil, err
	}

	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, cm, err := findVisibleChat(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		confirmed, err := confirmAttachmentInputs(ctx, tx, a.down.Media.Files, sub, body.Attachments)
		if err != nil {
			return err
		}
		at := now()
		actor := actorOf(sub)
		isExternalReply := cm.Scope == ChatExternal && !body.Note
		var replyToID *string
		if body.ReplyToItemID != "" {
			exists, err := tx.NewSelect().Table("timeline_items").
				Where("id = ? AND tenant_id = ? AND conversation_id = ?", body.ReplyToItemID, c.TenantID, c.ID).
				Exists(ctx)
			if err != nil {
				return errors.Wrap(err, "verify chat reply_to_item_id")
			}
			if !exists {
				return ErrNotFound.New()
			}
			rid := body.ReplyToItemID
			replyToID = &rid
		}
		if body.CorrectsItemID != "" {
			if body.Note {
				return ErrMessageNotCorrectable.New()
			}
			if _, err := validateCorrectionTarget(ctx, tx, sub, c, cm.Scope, body.CorrectsItemID); err != nil {
				return err
			}
		}
		var mentions []string
		if !isExternalReply {
			roles := authz.NewStore(tx, auth.Catalog)
			for i, m := range body.Mentions {
				if _, err := member.RoleOf(ctx, roles, identity.Ref{Type: member.Type, ID: m}, sub.Tenant); err != nil {
					if errors.Is(err, member.ErrNotFound) {
						return member.ErrNotFound.New(errorx.Field(fmt.Sprintf("mentions[%d]", i)))
					}
					return err
				}
			}
			mentions = body.Mentions
		}

		var targetChannelID *string
		if isExternalReply {
			targetChannelID = c.DeliveryChannelID()
			if targetChannelID == nil && cm.ChannelID != nil {
				targetChannelID = cm.ChannelID
			}
			if targetChannelID == nil {
				return ErrChannelUnavailable.New()
			}
			ch, err := channel.Find(ctx, tx, c.TenantID, *targetChannelID)
			if errors.Is(err, channel.ErrNotFound) {
				return ErrChannelUnavailable.New()
			}
			if err != nil {
				return err
			}
			if !ch.Enabled {
				return channel.ErrDisabled.New()
			}
		}

		if isExternalReply {
			extMentions, err := normalizeExternalMentions(ctx, tx, c, body.ExternalMentions)
			if err != nil {
				return err
			}
			item = buildOutboundMessageItem(actor, sub.ID, targetChannelID, replyToID, body.Text, extMentions, body.Translation, body.AutoTranslate)
		} else {
			kind := Message
			vis := Everyone
			if cm.Scope == ChatExternal && body.Note {
				kind = Note
				vis = MembersOnly
			}
			item = Item{
				Kind:          kind,
				ActorType:     actor,
				ActorID:       sub.ID,
				Visibility:    vis,
				ReplyToItemID: replyToID,
				Content:       Content{Text: body.Text, Mentions: mentions},
			}
			if err := applyVerifiedTranslation(ctx, tx, sub, &item, body.Translation); err != nil {
				return err
			}
		}

		c.LastMessageAt = at
		if err := commit(ctx, tx, a.down, c, &item); err != nil {
			return err
		}
		if body.CorrectsItemID != "" {
			if err := recordCorrection(ctx, tx, c, body.CorrectsItemID, item.ID, at); err != nil {
				return err
			}
		}
		if err := insertAttachments(ctx, tx, c.TenantID, item.ID, confirmed); err != nil {
			return err
		}
		if isExternalReply && targetChannelID != nil {
			if _, err := tx.NewInsert().Model(&Delivery{ItemID: item.ID, Status: Sending, UpdatedAt: at}).Exec(ctx); err != nil {
				return errors.Wrap(err, "insert external chat delivery")
			}
		}
		for _, m := range mentions {
			if _, err := tx.NewRaw(`INSERT INTO conversation_mentions (tenant_id, member_id, conversation_id, item_id, seq, created_at)
				VALUES (?, ?, ?, ?, ?, ?)`, sub.Tenant, m, c.ID, item.ID, item.Seq, item.CreatedAt).Exec(ctx); err != nil {
				return errors.Wrap(err, "insert chat mention")
			}
		}
		// 发送者自动推进本人已读位置
		if sub.Type == member.Type {
			if _, err := tx.NewRaw(`INSERT INTO conversation_reads (member_id, conversation_id, last_read_seq, updated_at) VALUES (?, ?, ?, ?)
				ON CONFLICT (member_id, conversation_id) DO UPDATE
				SET last_read_seq = GREATEST(conversation_reads.last_read_seq, EXCLUDED.last_read_seq), updated_at = EXCLUDED.updated_at`,
				sub.ID, c.ID, item.Seq, at).Exec(ctx); err != nil {
				return errors.Wrap(err, "advance sender chat read seq")
			}
		}
		return nil
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

// HTTP 处理 POST /t/{slug}/api/chats/{id}/messages。
func (a *SendChatMessage) HTTP(ctx context.Context, in *SendChatMessageInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}
