package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/tag"
)

// Actor 是接口中的参与者或发起者：类型和编号。
type Actor struct {
	Type ActorType `json:"type"`
	ID   string    `json:"id"`
}

// ConversationDTO 是接口中的会话。
type ConversationDTO struct {
	ID                string             `json:"id"`
	Kind              Kind               `json:"kind"`
	Status            Status             `json:"status"`
	Channel           *channel.Summary   `json:"channel,omitempty" doc:"会话来自的渠道"`
	Contact           *contact.Summary   `json:"contact,omitempty" doc:"对方：联系人"`
	TeamID            *string            `json:"team_id,omitempty" doc:"团队收件箱的 ID；为空表示未指派到团队"`
	Assignee          *Actor             `json:"assignee,omitempty" doc:"负责人；为空表示没有负责人"`
	Priority          Priority           `json:"priority"`
	Language          string             `json:"language" doc:"客户的语言：按最近几条确定了语言的消息判断，或者成员手动设置的"`
	WaitingSince      *time.Time         `json:"waiting_since,omitempty" doc:"对方的消息开始等待回复的时间；已回复时为空"`
	SnoozedUntil      *time.Time         `json:"snoozed_until,omitempty"`
	FirstReplyAt      *time.Time         `json:"first_reply_at,omitempty"`
	ClosedAt          *time.Time         `json:"closed_at,omitempty"`
	LastMessageAt     time.Time          `json:"last_message_at"`
	LastSeq           int64              `json:"last_seq" doc:"时间线中最新一项的序号，用来增量读取时间线"`
	Preview           string             `json:"preview" doc:"最后一条消息的文字：对方的消息是我的语言的译文（还没有时是原文），成员翻译后发出的回复是成员写的原文"`
	PreviewActor      *Actor             `json:"preview_actor,omitempty" doc:"最后一条消息是谁发的；还没有消息时为空"`
	PreviewAttachment *PreviewAttachment `json:"preview_attachment,omitempty" doc:"最后一条消息附带图片或文件时的摘要"`
	// PreviewItemID 和 PreviewUntranslated 让会话列表也按“看到时补译”：这一行出现在页面上时请求补译 PreviewItemID
	PreviewItemID       string         `json:"preview_item_id,omitempty" doc:"最后一条消息的 ID，只在它还缺我的语言的译文时给出"`
	PreviewUntranslated bool           `json:"preview_untranslated,omitempty" doc:"最后一条消息是对方发来、还缺我的语言的译文的：preview 是原文，页面看到这一行时请求补译"`
	Unread              bool           `json:"unread" doc:"有我还没读到的消息或内部备注（不算我自己写的）"`
	ReadSeq             int64          `json:"read_seq" doc:"我读到的位置：序号不大于它的项已读；会话页在它之后显示“新消息”分隔线"`
	MentionedAt         *time.Time     `json:"mentioned_at,omitempty" doc:"最近一次 @ 我的时间，只在“@我”的列表中有"`
	Rating              *Rating        `json:"rating,omitempty" doc:"客户最近一次的评价；没有评价过时为空"`
	Tags                []tag.Summary  `json:"tags" doc:"会话上的标签"`
	Attributes          map[string]any `json:"attributes" doc:"会话的自定义属性"`
	CreatedAt           time.Time      `json:"created_at"`
}

// ConversationOutput 是返回一个会话的响应。
type ConversationOutput struct {
	Body ConversationDTO
}

// ItemDTO 是接口中时间线的一项。
type ItemDTO struct {
	ID          string          `json:"id"`
	Seq         int64           `json:"seq"`
	Kind        ItemKind        `json:"kind"`
	Actor       Actor           `json:"actor" doc:"发起者"`
	Visibility  Visibility      `json:"visibility"`
	Content     Content         `json:"content"`
	Attachments []AttachmentDTO `json:"attachments,omitempty" doc:"附带的图片和文件"`
	Derived     map[string]any  `json:"derived" doc:"派生内容，例如按语言的译文，由后台补上"`
	Delivery    *DeliveryStatus `json:"delivery,omitempty" doc:"发给对方的消息的送达状态"`
	CreatedAt   time.Time       `json:"created_at"`
	// Untranslated 只在读取时间线时给出，按读取的成员的语言
	Untranslated bool `json:"untranslated,omitempty" doc:"对方发来、还没有我的语言的译文、也不是我的语言的消息：翻译可用时页面显示“翻译中”，并请求补译"`
}

// ItemOutput 是返回时间线一项的响应。
type ItemOutput struct {
	Body ItemDTO
}

// toItemDTOs 把时间线的各项转换为接口中的形式，一次读出它们的送达状态和附件。
func toItemDTOs(ctx context.Context, db bun.IDB, media Media, items []Item) ([]ItemDTO, error) {
	out := make([]ItemDTO, len(items))
	if len(items) == 0 {
		return out, nil
	}
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	var deliveries []Delivery
	if err := db.NewSelect().Model(&deliveries).Where("item_id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load deliveries")
	}
	status := make(map[string]DeliveryStatus, len(deliveries))
	for _, d := range deliveries {
		status[d.ItemID] = d.Status
	}
	attachments, err := loadAttachments(ctx, db, media, ids)
	if err != nil {
		return nil, err
	}
	for i, it := range items {
		out[i] = ItemDTO{
			ID: it.ID, Seq: it.Seq, Kind: it.Kind, Actor: Actor{Type: it.ActorType, ID: it.ActorID},
			Visibility: it.Visibility, Content: it.Content, Attachments: attachments[it.ID], Derived: it.Derived, CreatedAt: it.CreatedAt,
		}
		if s, ok := status[it.ID]; ok {
			out[i].Delivery = &s
		}
	}
	return out, nil
}

// toDTOs 把会话转换为接口中的形式：一次读出它们的渠道、联系人、最后一条消息的文字（对方的消息是 sub 的语言的译文），以及对 sub 来说是否未读。
func toDTOs(ctx context.Context, db bun.IDB, sub identity.Subject, convs []Conversation) ([]ConversationDTO, error) {
	out := make([]ConversationDTO, len(convs))
	if len(convs) == 0 {
		return out, nil
	}
	var ids, channelIDs, contactIDs []string
	for _, c := range convs {
		ids = append(ids, c.ID)
		if c.ChannelID != nil {
			channelIDs = append(channelIDs, *c.ChannelID)
		}
		if c.ContactID != nil {
			contactIDs = append(contactIDs, *c.ContactID)
		}
	}
	channels := map[string]channel.Summary{}
	if len(channelIDs) > 0 {
		var rows []channel.Channel
		if err := db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(channelIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load channels")
		}
		for i := range rows {
			channels[rows[i].ID] = rows[i].Summary()
		}
	}
	contacts := map[string]contact.Summary{}
	if len(contactIDs) > 0 {
		var rows []contact.Contact
		if err := db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(contactIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load contacts")
		}
		contactTags, err := tag.ForContacts(ctx, db, contactIDs)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			s := rows[i].Summary()
			if ts := contactTags[rows[i].ID]; len(ts) > 0 {
				s.Tags = ts
			}
			contacts[rows[i].ID] = s
		}
	}
	convTags, err := tag.ForConversations(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	// 每个会话的最后一条消息：对方的消息显示 sub 的语言的译文（还没有时显示原文，并标出还缺译文），成员翻译后发出的显示成员写的原文
	language, err := MemberLanguage(ctx, db, sub)
	if err != nil {
		return nil, err
	}
	var lasts []Item
	err = db.NewRaw(`SELECT DISTINCT ON (conversation_id) * FROM timeline_items
		WHERE conversation_id IN (?) AND kind = ? ORDER BY conversation_id, (actor_type = ?), seq DESC`, bun.In(ids), Message, SystemActor).Scan(ctx, &lasts)
	if err != nil {
		return nil, errors.Wrap(err, "load previews")
	}
	previews := make(map[string]*Item, len(lasts))
	lastIDs := make([]string, 0, len(lasts))
	for i := range lasts {
		previews[lasts[i].ConversationID] = &lasts[i]
		lastIDs = append(lastIDs, lasts[i].ID)
	}
	previewAttachments, err := loadPreviewAttachments(ctx, db, lastIDs)
	if err != nil {
		return nil, err
	}
	unread, err := unreadOf(ctx, db, sub, ids)
	if err != nil {
		return nil, err
	}
	readSeq, err := readSeqOf(ctx, db, sub, ids)
	if err != nil {
		return nil, err
	}
	for i, c := range convs {
		tags := convTags[c.ID]
		if tags == nil {
			tags = []tag.Summary{}
		}
		attrs := c.Attributes
		if attrs == nil {
			attrs = map[string]any{}
		}
		d := ConversationDTO{
			ID: c.ID, Kind: c.Kind, Status: c.Status, TeamID: c.TeamID, Priority: c.Priority, Language: c.Language,
			WaitingSince: c.WaitingSince, SnoozedUntil: c.SnoozedUntil, FirstReplyAt: c.FirstReplyAt, ClosedAt: c.ClosedAt,
			LastMessageAt: c.LastMessageAt, LastSeq: c.LastSeq, Unread: unread[c.ID], ReadSeq: readSeq[c.ID],
			MentionedAt: c.MentionedAt, Rating: c.rating(), Tags: tags, Attributes: attrs, CreatedAt: c.CreatedAt,
		}
		if p := previews[c.ID]; p != nil {
			d.Preview, d.PreviewActor = previewText(p, language), &Actor{Type: p.ActorType, ID: p.ActorID}
			d.PreviewAttachment = previewAttachments[p.ID]
			if untranslated(p, language) {
				d.PreviewItemID, d.PreviewUntranslated = p.ID, true
			}
		}
		if c.ChannelID != nil {
			s := channels[*c.ChannelID]
			d.Channel = &s
		}
		if c.ContactID != nil {
			s := contacts[*c.ContactID]
			d.Contact = &s
			// 有联系人时，客户的语言以联系人为准，同一联系人的多个会话始终一致
			d.Language = s.Language
		}
		if c.AssigneeType != "" {
			d.Assignee = &Actor{Type: c.AssigneeType, ID: c.AssigneeID}
		}
		out[i] = d
	}
	return out, nil
}

// previewText 返回会话列表中最后一条消息 it 给语言是 language 的成员看的文字：对方的消息有这种语言的译文时是译文，
// 成员翻译后发出的回复是成员写的原文，其他情况是消息的文字。
func previewText(it *Item, language string) string {
	if it.ActorType == ContactActor {
		if translations, _ := it.Derived["translations"].(map[string]any); translations != nil {
			if t, _ := translations[language].(string); t != "" {
				return t
			}
		}
		return it.Content.Text
	}
	if it.Content.Original != "" {
		return it.Content.Original
	}
	return it.Content.Text
}
