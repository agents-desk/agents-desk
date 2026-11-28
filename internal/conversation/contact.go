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

// ContactAuthor 是联系人看到的一项的发起者。
type ContactAuthor struct {
	Type ActorType `json:"type" doc:"contact 是联系人自己，member 是成员，bot 是 AI 机器人，system 是系统"`
	Name string    `json:"name" doc:"成员或机器人的名字；联系人自己和系统为空"`
}

// ContactItemDTO 是联系人看到的时间线中的一项：对所有人可见的消息。
type ContactItemDTO struct {
	ID             string           `json:"id"`
	ConversationID string           `json:"conversation_id,omitempty" doc:"该消息所属的服务型会话 ID"`
	Seq            int64            `json:"seq"`
	Kind           ItemKind         `json:"kind"`
	Author         ContactAuthor    `json:"author"`
	Channel        *channel.Summary `json:"channel,omitempty" doc:"该消息来自或发往的渠道"`
	Text           string           `json:"text"`
	Attachments    []AttachmentDTO  `json:"attachments,omitempty" doc:"附带的图片和文件"`
	Prompt         *Prompt          `json:"prompt,omitempty" doc:"系统发出的结构化提示或交互卡片"`
	CorrectedBy    *string          `json:"corrected_by,omitempty" doc:"更正这条消息的新消息 ID；非空表示这条消息已被更正"`
	CorrectedAt    *time.Time       `json:"corrected_at,omitempty" doc:"被更正的时间"`
	CorrectsItemID *string          `json:"corrects_item_id,omitempty" doc:"这条消息更正的原消息 ID"`
	CreatedAt      time.Time        `json:"created_at"`
}

// findForContact 读取联系人 sub 在渠道 channelID 中可见的会话 id。
// 个人商务沟通链接（owner_member_id 非空）仅返回该渠道下的外部商务聊天（kind=chat）；
// 公共网页聊天插件中，已登录用户（contact.User）可跨渠道访问本工作区名下全部服务型会话，访客线索仅限当前渠道。
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
	targetKind, restrictChannel, err := contactChannelScope(ctx, db, sub.Tenant, channelID, kind)
	if err != nil {
		return nil, err
	}
	c := new(Conversation)
	q := db.NewSelect().Model(c).Where("id = ? AND tenant_id = ? AND kind = ? AND contact_id = ?", id, sub.Tenant, targetKind, sub.ID)
	if restrictChannel {
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

// contactChannelScope 返回联系人通过渠道 channelID 访问连续时间线时对应的会话种类与是否限定到本渠道：
// 个人商务渠道（owner_member_id 非空）限定为 kind=chat 且仅看本渠道；公共客服渠道为 kind=service，线索限定到本渠道、用户跨渠道可见。
func contactChannelScope(ctx context.Context, db bun.IDB, tenantID, channelID string, ck contact.Kind) (Kind, bool, error) {
	var owner *string
	err := db.NewSelect().Table("channels").Column("owner_member_id").
		Where("id = ? AND tenant_id = ?", channelID, tenantID).Scan(ctx, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return Service, ck != contact.User, nil
	}
	if err != nil {
		return Service, false, errors.Wrap(err, "load channel owner for contact scope")
	}
	if owner != nil && *owner != "" {
		return Chat, true, nil
	}
	return Service, ck != contact.User, nil
}

// toContactItemDTOs 把时间线的各项转换为联系人看到的形式，一次读出成员的名字、所属渠道和签过名的附件地址。
func toContactItemDTOs(ctx context.Context, db bun.IDB, media Media, channelID string, items []Item) ([]ContactItemDTO, error) {
	out := make([]ContactItemDTO, len(items))
	var memberIDs, botIDs, chIDs []string
	seenCh := map[string]bool{}
	itemIDs := make([]string, len(items))
	for i, it := range items {
		itemIDs[i] = it.ID
		switch it.ActorType {
		case MemberActor:
			memberIDs = append(memberIDs, it.ActorID)
		case BotActor:
			botIDs = append(botIDs, it.ActorID)
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
	if len(botIDs) > 0 {
		var rows []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("bots").ColumnExpr("id::text AS id, name").Where("id IN (?)", bun.In(botIDs)).Scan(ctx, &rows); err != nil {
			return nil, errors.Wrap(err, "load bot names for contact")
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
	correctedBy, correctsItem, err := loadCorrections(ctx, db, itemIDs)
	if err != nil {
		return nil, err
	}
	for i, it := range items {
		out[i] = ContactItemDTO{
			ID: it.ID, ConversationID: it.ConversationID, Seq: it.Seq, Kind: it.Kind, Author: ContactAuthor{Type: it.ActorType, Name: names[it.ActorID]},
			Text: it.Content.Text, Attachments: SignContactAttachments(attachments[it.ID], channelID, media.Crypt), Prompt: it.Content.Prompt, CreatedAt: it.CreatedAt,
		}
		if it.ChannelID != nil {
			if s, ok := channels[*it.ChannelID]; ok {
				out[i].Channel = &s
			}
		}
		if corr, ok := correctedBy[it.ID]; ok {
			by := corr.CorrectionItemID
			at := corr.CreatedAt
			out[i].CorrectedBy = &by
			out[i].CorrectedAt = &at
		}
		if origID, ok := correctsItem[it.ID]; ok {
			orig := origID
			out[i].CorrectsItemID = &orig
		}
	}
	return out, nil
}

// ForContact 返回联系人自己发的消息 d 在联系人看到的形式，例如聊天插件发消息后的响应。
func (d ItemDTO) ForContact() ContactItemDTO {
	return ContactItemDTO{
		ID: d.ID, Seq: d.Seq, Kind: d.Kind, Author: ContactAuthor{Type: d.Actor.Type}, Channel: d.Channel,
		Text: d.Content.Text, Attachments: d.Attachments, Prompt: d.Content.Prompt,
		CorrectedBy: d.CorrectedBy, CorrectedAt: d.CorrectedAt, CorrectsItemID: d.CorrectsItemID,
		CreatedAt: d.CreatedAt,
	}
}
