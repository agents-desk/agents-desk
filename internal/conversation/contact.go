package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
)

// ContactConversationDTO 是联系人在聊天插件等渠道中看到的自己的会话：没有团队收件箱、负责人和内部备注。
type ContactConversationDTO struct {
	ID                string             `json:"id"`
	Status            Status             `json:"status" doc:"打开或已关闭；稍后处理的会话对客户显示为打开"`
	RatingRequested   bool               `json:"rating_requested" doc:"这次关闭是否向客户发出了评价邀请；为真且 rating 为空时可以评价"`
	Rating            *Rating            `json:"rating,omitempty" doc:"客户对最近一次关闭的评价；会话已关闭且 rating_requested 为真、它为空时可以评价"`
	Preview           string             `json:"preview" doc:"最后一条消息的文字"`
	PreviewPrompt     *Prompt            `json:"preview_prompt,omitempty" doc:"最后一条消息的结构化提示；不为空时前端按客户的语言渲染"`
	PreviewAttachment *PreviewAttachment `json:"preview_attachment,omitempty" doc:"最后一条消息附带图片或文件时的摘要"`
	PreviewAuthor     ContactAuthor      `json:"preview_author" doc:"最后一条消息是谁发的"`
	Unread            int                `json:"unread" doc:"成员发来、联系人还没读到的消息数"`
	LastMessageAt     time.Time          `json:"last_message_at"`
	LastSeq           int64              `json:"last_seq" doc:"时间线中最新一项的序号；联系人看到的时间线跳过内部备注和系统事件，序号不连续"`
	CreatedAt         time.Time          `json:"created_at"`
}

// ContactAuthor 是联系人看到的一项的发起者。
type ContactAuthor struct {
	Type ActorType `json:"type" doc:"contact 是联系人自己，member 是成员，system 是系统"`
	Name string    `json:"name" doc:"成员的名字；联系人自己和系统为空"`
}

// ContactItemDTO 是联系人看到的时间线中的一项：对所有人可见的消息。
type ContactItemDTO struct {
	ID          string           `json:"id"`
	Seq         int64            `json:"seq"`
	Kind        ItemKind         `json:"kind"`
	Author      ContactAuthor    `json:"author"`
	Channel     *channel.Summary `json:"channel,omitempty" doc:"该消息来自或发往的渠道"`
	Text        string           `json:"text"`
	Attachments []AttachmentDTO  `json:"attachments,omitempty" doc:"附带的图片和文件"`
	Prompt      *Prompt          `json:"prompt,omitempty" doc:"系统发出的结构化提示或交互卡片"`
	CreatedAt   time.Time        `json:"created_at"`
}

// findForContact 读取联系人 sub 在渠道 channelID 中可见的会话 id。
// 已登录用户（contact.User）可跨渠道访问自己在当前工作区名下的所有服务型会话；
// 访客线索（contact.Lead）只允许访问来源渠道为 channelID 的会话。
// sub 不是联系人时返回 ErrForbidden；会话不存在、属于别的工作区、别的联系人或不可见时返回 ErrNotFound。
func findForContact(ctx context.Context, db bun.IDB, sub identity.Subject, channelID, id string, lock bool) (*Conversation, error) {
	if sub.Type != contact.Type {
		return nil, ErrForbidden.New()
	}
	var kind contact.Kind
	err := db.NewSelect().Table("contacts").Column("kind").Where("id = ? AND tenant_id = ?", sub.ID, sub.Tenant).Scan(ctx, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "load contact kind")
	}
	c := new(Conversation)
	q := db.NewSelect().Model(c).Where("id = ? AND tenant_id = ? AND kind = ? AND contact_id = ?", id, sub.Tenant, Service, sub.ID)
	if kind != contact.User {
		q = q.Where("channel_id = ?", channelID)
	}
	if lock {
		q = q.For("UPDATE")
	}
	err = q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return c, errors.Wrap(err, "load conversation")
}

// toContactDTOs 把会话转换为联系人看到的形式：一次读出最后一条消息的文字和联系人未读的消息数。
func toContactDTOs(ctx context.Context, db bun.IDB, convs []Conversation) ([]ContactConversationDTO, error) {
	out := make([]ContactConversationDTO, len(convs))
	if len(convs) == 0 {
		return out, nil
	}
	ids := make([]string, len(convs))
	for i, c := range convs {
		ids[i] = c.ID
	}
	var rows []struct {
		ConversationID string    `bun:"conversation_id"`
		LastItemID     string    `bun:"last_item_id"`
		Preview        string    `bun:"preview"`
		PreviewPrompt  *Prompt   `bun:"preview_prompt,type:jsonb"`
		ActorType      ActorType `bun:"actor_type"`
		ActorName      string    `bun:"actor_name"`
		Unread         int       `bun:"unread"`
	}
	err := db.NewRaw(`SELECT c.id AS conversation_id, COALESCE(last.id::text, '') AS last_item_id, COALESCE(last.text, '') AS preview, last.prompt AS preview_prompt,
			COALESCE(last.actor_type, '') AS actor_type, COALESCE(m.name, '') AS actor_name,
			(SELECT count(*) FROM timeline_items t JOIN message_deliveries d ON d.item_id = t.id
				WHERE t.conversation_id = c.id AND t.actor_type <> ? AND d.status <> ?) AS unread
		FROM conversations c
		LEFT JOIN LATERAL (SELECT t.id, t.content->>'text' AS text, t.content->'prompt' AS prompt, t.actor_type, t.actor_id FROM timeline_items t
			WHERE t.conversation_id = c.id AND t.kind = ? AND t.visibility = ? ORDER BY t.seq DESC LIMIT 1) last ON true
		LEFT JOIN members m ON last.actor_type = ? AND m.id::text = last.actor_id
		WHERE c.id IN (?)`, ContactActor, Read, Message, Everyone, MemberActor, bun.In(ids)).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load contact previews")
	}
	extra := make(map[string]int, len(rows))
	lastItemIDs := make([]string, 0, len(rows))
	for i, r := range rows {
		extra[r.ConversationID] = i
		if r.LastItemID != "" {
			lastItemIDs = append(lastItemIDs, r.LastItemID)
		}
	}
	previewAttachments, err := loadPreviewAttachments(ctx, db, lastItemIDs)
	if err != nil {
		return nil, err
	}
	for i, c := range convs {
		r := rows[extra[c.ID]]
		out[i] = ContactConversationDTO{
			ID: c.ID, Status: c.Status, Preview: r.Preview, PreviewPrompt: r.PreviewPrompt, PreviewAttachment: previewAttachments[r.LastItemID],
			PreviewAuthor: ContactAuthor{Type: r.ActorType, Name: r.ActorName}, Unread: r.Unread,
			LastMessageAt: c.LastMessageAt, LastSeq: c.LastSeq, CreatedAt: c.CreatedAt,
		}
		// 稍后处理是团队内部的安排，客户看到的是打开
		if c.Status == Snoozed {
			out[i].Status = Open
		}
		if c.Status == Closed {
			out[i].RatingRequested = c.ratingRequestedSinceClosed()
			if c.ratedSinceClosed() {
				out[i].Rating = c.rating()
			}
		}
	}
	return out, nil
}

// toContactItemDTOs 把时间线的各项转换为联系人看到的形式，一次读出成员的名字、所属渠道和附件。
func toContactItemDTOs(ctx context.Context, db bun.IDB, media Media, items []Item) ([]ContactItemDTO, error) {
	out := make([]ContactItemDTO, len(items))
	var memberIDs, chIDs []string
	seenCh := map[string]bool{}
	itemIDs := make([]string, len(items))
	for i, it := range items {
		itemIDs[i] = it.ID
		if it.ActorType == MemberActor {
			memberIDs = append(memberIDs, it.ActorID)
		}
		if it.ChannelID != nil && !seenCh[*it.ChannelID] {
			seenCh[*it.ChannelID] = true
			chIDs = append(chIDs, *it.ChannelID)
		}
	}
	names := map[string]string{}
	if len(memberIDs) > 0 {
		var rows []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("members").Column("id", "name").Where("id IN (?)", bun.In(memberIDs)).Scan(ctx, &rows); err != nil {
			return nil, errors.Wrap(err, "load member names")
		}
		for _, r := range rows {
			names[r.ID] = r.Name
		}
	}
	channels := map[string]channel.Summary{}
	if len(chIDs) > 0 {
		var rows []channel.Channel
		if err := db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(chIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load contact item channels")
		}
		for i := range rows {
			channels[rows[i].ID] = rows[i].Summary()
		}
	}
	attachments, err := loadAttachments(ctx, db, media, itemIDs)
	if err != nil {
		return nil, err
	}
	for i, it := range items {
		out[i] = ContactItemDTO{
			ID: it.ID, Seq: it.Seq, Kind: it.Kind, Author: ContactAuthor{Type: it.ActorType, Name: names[it.ActorID]},
			Text: it.Content.Text, Attachments: attachments[it.ID], Prompt: it.Content.Prompt, CreatedAt: it.CreatedAt,
		}
		if it.ChannelID != nil {
			if s, ok := channels[*it.ChannelID]; ok {
				out[i].Channel = &s
			}
		}
	}
	return out, nil
}

// ForContact 返回联系人自己发的消息 d 在联系人看到的形式，例如聊天插件发消息后的响应。
func (d ItemDTO) ForContact() ContactItemDTO {
	return ContactItemDTO{
		ID: d.ID, Seq: d.Seq, Kind: d.Kind, Author: ContactAuthor{Type: d.Actor.Type}, Channel: d.Channel,
		Text: d.Content.Text, Attachments: d.Attachments, Prompt: d.Content.Prompt, CreatedAt: d.CreatedAt,
	}
}
