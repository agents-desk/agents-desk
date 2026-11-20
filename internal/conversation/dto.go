package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
)

// Actor 是接口中的参与者或发起者：类型和编号。
type Actor struct {
	Type ActorType `json:"type"`
	ID   string    `json:"id"`
}

// ConversationDTO 是接口中的会话。
type ConversationDTO struct {
	ID            string           `json:"id"`
	Kind          Kind             `json:"kind"`
	Status        Status           `json:"status"`
	Channel       *channel.Summary `json:"channel,omitempty" doc:"会话来自的渠道"`
	Contact       *contact.Summary `json:"contact,omitempty" doc:"对方：联系人"`
	TeamID        *string          `json:"team_id,omitempty" doc:"团队收件箱的 ID；为空表示未指派到团队"`
	Assignee      *Actor           `json:"assignee,omitempty" doc:"负责人；为空表示没有负责人"`
	Priority      Priority         `json:"priority"`
	Language      string           `json:"language" doc:"对方最近一条消息识别出的语言"`
	WaitingSince  *time.Time       `json:"waiting_since,omitempty" doc:"对方的消息开始等待回复的时间；已回复时为空"`
	SnoozedUntil  *time.Time       `json:"snoozed_until,omitempty"`
	FirstReplyAt  *time.Time       `json:"first_reply_at,omitempty"`
	ClosedAt      *time.Time       `json:"closed_at,omitempty"`
	LastMessageAt time.Time        `json:"last_message_at"`
	LastSeq       int64            `json:"last_seq" doc:"时间线中最新一项的序号，用来增量读取时间线"`
	Preview       string           `json:"preview" doc:"最后一条消息的文字"`
	Unread        bool             `json:"unread" doc:"有我还没读到的消息或内部备注（不算我自己写的）"`
	MentionedAt   *time.Time       `json:"mentioned_at,omitempty" doc:"最近一次 @ 我的时间，只在“@我”的列表中有"`
	CreatedAt     time.Time        `json:"created_at"`
}

// ConversationOutput 是返回一个会话的响应。
type ConversationOutput struct {
	Body ConversationDTO
}

// ItemDTO 是接口中时间线的一项。
type ItemDTO struct {
	ID         string          `json:"id"`
	Seq        int64           `json:"seq"`
	Kind       ItemKind        `json:"kind"`
	Actor      Actor           `json:"actor" doc:"发起者"`
	Visibility Visibility      `json:"visibility"`
	Content    Content         `json:"content"`
	Derived    map[string]any  `json:"derived" doc:"派生内容，例如按语言的译文，由后台补上"`
	Delivery   *DeliveryStatus `json:"delivery,omitempty" doc:"发给对方的消息的送达状态"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ItemOutput 是返回时间线一项的响应。
type ItemOutput struct {
	Body ItemDTO
}

// toItemDTOs 把时间线的各项转换为接口中的形式，一次读出它们的送达状态。
func toItemDTOs(ctx context.Context, db bun.IDB, items []Item) ([]ItemDTO, error) {
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
	for i, it := range items {
		out[i] = ItemDTO{
			ID: it.ID, Seq: it.Seq, Kind: it.Kind, Actor: Actor{Type: it.ActorType, ID: it.ActorID},
			Visibility: it.Visibility, Content: it.Content, Derived: it.Derived, CreatedAt: it.CreatedAt,
		}
		if s, ok := status[it.ID]; ok {
			out[i].Delivery = &s
		}
	}
	return out, nil
}

// toDTOs 把会话转换为接口中的形式：一次读出它们的渠道、联系人、最后一条消息的文字，以及对 sub 来说是否未读。
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
		for i := range rows {
			contacts[rows[i].ID] = rows[i].Summary()
		}
	}
	var previews []struct {
		ConversationID string `bun:"conversation_id"`
		Text           string `bun:"text"`
	}
	err := db.NewRaw(`SELECT DISTINCT ON (conversation_id) conversation_id, content->>'text' AS text FROM timeline_items
		WHERE conversation_id IN (?) AND kind = ? ORDER BY conversation_id, seq DESC`, bun.In(ids), Message).Scan(ctx, &previews)
	if err != nil {
		return nil, errors.Wrap(err, "load previews")
	}
	preview := make(map[string]string, len(previews))
	for _, p := range previews {
		preview[p.ConversationID] = p.Text
	}
	unread, err := unreadOf(ctx, db, sub, ids)
	if err != nil {
		return nil, err
	}
	for i, c := range convs {
		d := ConversationDTO{
			ID: c.ID, Kind: c.Kind, Status: c.Status, TeamID: c.TeamID, Priority: c.Priority, Language: c.Language,
			WaitingSince: c.WaitingSince, SnoozedUntil: c.SnoozedUntil, FirstReplyAt: c.FirstReplyAt, ClosedAt: c.ClosedAt,
			LastMessageAt: c.LastMessageAt, LastSeq: c.LastSeq, Preview: preview[c.ID], Unread: unread[c.ID],
			MentionedAt: c.MentionedAt, CreatedAt: c.CreatedAt,
		}
		if c.ChannelID != nil {
			s := channels[*c.ChannelID]
			d.Channel = &s
		}
		if c.ContactID != nil {
			s := contacts[*c.ContactID]
			d.Contact = &s
		}
		if c.AssigneeType != "" {
			d.Assignee = &Actor{Type: c.AssigneeType, ID: c.AssigneeID}
		}
		out[i] = d
	}
	return out, nil
}
