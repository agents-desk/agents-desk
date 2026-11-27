package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/extgroup"
	"agents-desk/internal/tag"
)

// Actor 是接口中的参与者或发起者：类型、编号、可选显示名称与外部平台标识（外部联系人或群发言人携带姓名，便于群聊展示与 @ 提及）。
type Actor struct {
	Type       ActorType `json:"type"`
	ID         string    `json:"id"`
	Name       string    `json:"name,omitempty"`
	ExternalID string    `json:"external_id,omitempty"`
}

// ConversationDTO 是接口中的会话。
type ConversationDTO struct {
	ID                string             `json:"id"`
	Kind              Kind               `json:"kind"`
	Audience          channel.Audience   `json:"audience,omitempty" doc:"服务对象类型：customer（外部客户）、partner（外部商务伙伴）或 internal（内部员工）"`
	Status            Status             `json:"status"`
	Channel           *channel.Summary   `json:"channel,omitempty" doc:"会话最初发起的来源渠道"`
	ReplyChannel      *channel.Summary   `json:"reply_channel,omitempty" doc:"会话当前的默认回复渠道"`
	Channels          []channel.Summary  `json:"channels,omitempty" doc:"会话中出现过的全部渠道（来源渠道优先，去重）"`
	AvailableChannels []channel.Summary  `json:"available_channels,omitempty" doc:"该联系人当前已绑定且已启用的同服务对象可回复渠道列表"`
	Contact           *contact.Summary   `json:"contact,omitempty" doc:"对方：联系人（1:1 会话）"`
	ExternalGroup     *extgroup.Summary  `json:"external_group,omitempty" doc:"对方：外部群（群聊机器人会话）"`
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
	ID            string           `json:"id"`
	Seq           int64            `json:"seq"`
	Kind          ItemKind         `json:"kind"`
	Actor         Actor            `json:"actor" doc:"发起者"`
	Visibility    Visibility       `json:"visibility"`
	Channel       *channel.Summary `json:"channel,omitempty" doc:"该消息来自或发往的渠道"`
	ReplyToItemID *string          `json:"reply_to_item_id,omitempty" doc:"引用回复的时间线项 ID"`
	Content       Content          `json:"content"`
	Attachments   []AttachmentDTO  `json:"attachments,omitempty" doc:"附带的图片和文件"`
	Derived       map[string]any   `json:"derived" doc:"派生内容，例如按语言的译文，由后台补上"`
	Delivery      *DeliveryStatus  `json:"delivery,omitempty" doc:"发给对方的消息的送达状态"`
	CreatedAt     time.Time        `json:"created_at"`
	// Untranslated 只在读取时间线时给出，按读取的成员的语言
	Untranslated bool `json:"untranslated,omitempty" doc:"对方发来、还没有我的语言的译文、也不是我的语言的消息：翻译可用时页面显示“翻译中”，并请求补译"`
	// Revision 是编辑次数；0 表示没有编辑过。正文已经是最新一版
	Revision int `json:"revision,omitempty" doc:"编辑次数；0 表示没有编辑过"`
	// EditedAt 是最近一次编辑的时间
	EditedAt *time.Time `json:"edited_at,omitempty" doc:"最近一次编辑的时间"`
}

// ItemOutput 是返回时间线一项的响应。
type ItemOutput struct {
	Body ItemDTO
}

// toItemDTOs 把时间线的各项转换为接口中的形式，一次读出它们的送达状态、所属渠道、发起者名称和附件。
func toItemDTOs(ctx context.Context, db bun.IDB, media Media, items []Item) ([]ItemDTO, error) {
	out := make([]ItemDTO, len(items))
	if len(items) == 0 {
		return out, nil
	}
	ids := make([]string, len(items))
	var chIDs, contactActorIDs, extUserActorIDs []string
	seenCh := map[string]bool{}
	seenCt := map[string]bool{}
	seenEU := map[string]bool{}
	for i := range items {
		ids[i] = items[i].ID
		if items[i].ChannelID != nil && !seenCh[*items[i].ChannelID] {
			seenCh[*items[i].ChannelID] = true
			chIDs = append(chIDs, *items[i].ChannelID)
		}
		if items[i].ActorType == ContactActor && items[i].ActorID != "" && !seenCt[items[i].ActorID] {
			seenCt[items[i].ActorID] = true
			contactActorIDs = append(contactActorIDs, items[i].ActorID)
		}
		if items[i].ActorType == ExternalUserActor && items[i].ActorID != "" && !seenEU[items[i].ActorID] {
			seenEU[items[i].ActorID] = true
			extUserActorIDs = append(extUserActorIDs, items[i].ActorID)
		}
	}
	var deliveries []Delivery
	if err := db.NewSelect().Model(&deliveries).Where("item_id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load deliveries")
	}
	status := make(map[string]DeliveryStatus, len(deliveries))
	for _, d := range deliveries {
		status[d.ItemID] = d.Status
	}
	channels := map[string]channel.Summary{}
	if len(chIDs) > 0 {
		var rows []channel.Channel
		if err := db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(chIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load item channels")
		}
		for i := range rows {
			channels[rows[i].ID] = rows[i].Summary()
		}
	}
	contactNames := map[string]string{}
	if len(contactActorIDs) > 0 {
		var rows []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("contacts").Column("id", "name").Where("id IN (?)", bun.In(contactActorIDs)).Scan(ctx, &rows); err != nil {
			return nil, errors.Wrap(err, "load item contact names")
		}
		for _, r := range rows {
			if r.Name != "" {
				contactNames[r.ID] = r.Name
			}
		}
	}
	type extUserInfo struct {
		Name       string
		ExternalID string
	}
	extUsers := map[string]extUserInfo{}
	if len(extUserActorIDs) > 0 {
		var rows []struct {
			ID         string `bun:"id"`
			Name       string `bun:"name"`
			ExternalID string `bun:"external_id"`
		}
		if err := db.NewSelect().Table("external_users").Column("id", "name", "external_id").Where("id IN (?)", bun.In(extUserActorIDs)).Scan(ctx, &rows); err != nil {
			return nil, errors.Wrap(err, "load item external users")
		}
		for _, r := range rows {
			extUsers[r.ID] = extUserInfo{Name: r.Name, ExternalID: r.ExternalID}
		}
	}
	attachments, err := loadAttachments(ctx, db, media, ids)
	if err != nil {
		return nil, err
	}
	// 编辑过的消息取最新一版正文；时间线项本身保持原样
	type revisionRow struct {
		ItemID    string    `bun:"item_id"`
		Revision  int       `bun:"revision"`
		Text      string    `bun:"text"`
		CreatedAt time.Time `bun:"created_at"`
	}
	var revisions []revisionRow
	if err := db.NewRaw(`SELECT DISTINCT ON (item_id) item_id, revision, text, created_at
		FROM message_revisions WHERE item_id IN (?) ORDER BY item_id, revision DESC`, bun.In(ids)).
		Scan(ctx, &revisions); err != nil {
		return nil, errors.Wrap(err, "load message revisions")
	}
	latestRevision := make(map[string]revisionRow, len(revisions))
	for _, r := range revisions {
		latestRevision[r.ItemID] = r
	}
	for i, it := range items {
		actor := Actor{Type: it.ActorType, ID: it.ActorID}
		switch it.ActorType {
		case ContactActor:
			actor.Name = contactNames[it.ActorID]
		case ExternalUserActor:
			if info, ok := extUsers[it.ActorID]; ok {
				actor.Name = info.Name
				actor.ExternalID = info.ExternalID
			}
		}
		out[i] = ItemDTO{
			ID: it.ID, Seq: it.Seq, Kind: it.Kind, Actor: actor,
			Visibility: it.Visibility, ReplyToItemID: it.ReplyToItemID,
			Content: it.Content, Attachments: attachments[it.ID], Derived: it.Derived, CreatedAt: it.CreatedAt,
		}
		if it.ChannelID != nil {
			if s, ok := channels[*it.ChannelID]; ok {
				out[i].Channel = &s
			}
		}
		if s, ok := status[it.ID]; ok {
			out[i].Delivery = &s
		}
		if rev, ok := latestRevision[it.ID]; ok {
			out[i].Content.Text = rev.Text
			out[i].Revision = rev.Revision
			editedAt := rev.CreatedAt
			out[i].EditedAt = &editedAt
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
	var ids, channelIDs, contactIDs, extGroupIDs []string
	seenCh := map[string]bool{}
	seenGroup := map[string]bool{}
	addChID := func(id string) {
		if id != "" && !seenCh[id] {
			seenCh[id] = true
			channelIDs = append(channelIDs, id)
		}
	}
	for _, c := range convs {
		ids = append(ids, c.ID)
		if c.ChannelID != nil {
			addChID(*c.ChannelID)
		}
		if c.ReplyChannelID != nil {
			addChID(*c.ReplyChannelID)
		}
		if c.ContactID != nil {
			contactIDs = append(contactIDs, *c.ContactID)
		}
		if c.ExternalGroupID != nil && *c.ExternalGroupID != "" && !seenGroup[*c.ExternalGroupID] {
			seenGroup[*c.ExternalGroupID] = true
			extGroupIDs = append(extGroupIDs, *c.ExternalGroupID)
		}
	}
	// 查出各会话时间线中出现过的渠道（按首次出现顺序）
	var itemChRows []struct {
		ConversationID string `bun:"conversation_id"`
		ChannelID      string `bun:"channel_id"`
	}
	if err := db.NewRaw(`SELECT conversation_id, channel_id FROM timeline_items
		WHERE conversation_id IN (?) AND channel_id IS NOT NULL
		GROUP BY conversation_id, channel_id
		ORDER BY conversation_id, min(seq) ASC`, bun.In(ids)).Scan(ctx, &itemChRows); err != nil {
		return nil, errors.Wrap(err, "load conversation item channels")
	}
	convChannelIDs := map[string][]string{}
	for _, r := range itemChRows {
		convChannelIDs[r.ConversationID] = append(convChannelIDs[r.ConversationID], r.ChannelID)
		addChID(r.ChannelID)
	}
	// 查出关联联系人当前已绑定且已启用的公共服务渠道，作为可回复渠道列表
	availableByContact := map[string][]string{}
	if len(contactIDs) > 0 {
		var identRows []struct {
			ContactID string `bun:"contact_id"`
			ChannelID string `bun:"channel_id"`
		}
		if err := db.NewRaw(`SELECT ci.contact_id, ci.channel_id
			FROM contact_identities ci
			JOIN channels ch ON ch.id = ci.channel_id
			WHERE ci.tenant_id = ? AND ci.contact_id IN (?) AND ch.enabled = true AND ch.owner_member_id IS NULL
			GROUP BY ci.contact_id, ci.channel_id
			ORDER BY ci.contact_id, min(ci.created_at) ASC`, sub.Tenant, bun.In(contactIDs)).Scan(ctx, &identRows); err != nil {
			return nil, errors.Wrap(err, "load contact available channels")
		}
		for _, r := range identRows {
			availableByContact[r.ContactID] = append(availableByContact[r.ContactID], r.ChannelID)
			addChID(r.ChannelID)
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
	extGroups := map[string]extgroup.Summary{}
	if len(extGroupIDs) > 0 {
		var rows []extgroup.Group
		if err := db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(extGroupIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load external groups")
		}
		for i := range rows {
			extGroups[rows[i].ID] = rows[i].Summary()
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
	var previewExtUserIDs []string
	seenPreviewEU := map[string]bool{}
	for i := range lasts {
		previews[lasts[i].ConversationID] = &lasts[i]
		lastIDs = append(lastIDs, lasts[i].ID)
		if lasts[i].ActorType == ExternalUserActor && lasts[i].ActorID != "" && !seenPreviewEU[lasts[i].ActorID] {
			seenPreviewEU[lasts[i].ActorID] = true
			previewExtUserIDs = append(previewExtUserIDs, lasts[i].ActorID)
		}
	}
	type previewEUInfo struct {
		Name       string
		ExternalID string
	}
	previewExtUsers := map[string]previewEUInfo{}
	if len(previewExtUserIDs) > 0 {
		var rows []struct {
			ID         string `bun:"id"`
			Name       string `bun:"name"`
			ExternalID string `bun:"external_id"`
		}
		if err := db.NewSelect().Table("external_users").Column("id", "name", "external_id").Where("id IN (?)", bun.In(previewExtUserIDs)).Scan(ctx, &rows); err != nil {
			return nil, errors.Wrap(err, "load preview external users")
		}
		for _, r := range rows {
			previewExtUsers[r.ID] = previewEUInfo{Name: r.Name, ExternalID: r.ExternalID}
		}
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
			ID: c.ID, Kind: c.Kind, Audience: c.Audience, Status: c.Status, TeamID: c.TeamID, Priority: c.Priority, Language: c.Language,
			WaitingSince: c.WaitingSince, SnoozedUntil: c.SnoozedUntil, FirstReplyAt: c.FirstReplyAt, ClosedAt: c.ClosedAt,
			LastMessageAt: c.LastMessageAt, LastSeq: c.LastSeq, Unread: unread[c.ID], ReadSeq: readSeq[c.ID],
			MentionedAt: c.MentionedAt, Rating: c.rating(), Tags: tags, Attributes: attrs, CreatedAt: c.CreatedAt,
		}
		if p := previews[c.ID]; p != nil {
			actor := Actor{Type: p.ActorType, ID: p.ActorID}
			switch p.ActorType {
			case ContactActor:
				if ct, ok := contacts[p.ActorID]; ok {
					actor.Name = ct.Name
				}
			case ExternalUserActor:
				if info, ok := previewExtUsers[p.ActorID]; ok {
					actor.Name = info.Name
					actor.ExternalID = info.ExternalID
				}
			}
			d.Preview, d.PreviewActor = previewText(p, sub.ID, language), &actor
			d.PreviewAttachment = previewAttachments[p.ID]
			if untranslated(p, sub.ID, language) {
				d.PreviewItemID, d.PreviewUntranslated = p.ID, true
			}
		}
		if c.ChannelID != nil {
			s := channels[*c.ChannelID]
			d.Channel = &s
		}
		if replyID := c.DeliveryChannelID(); replyID != nil {
			s := channels[*replyID]
			d.ReplyChannel = &s
		}
		// 组装会话涉及的全部渠道（来源渠道优先，随后按消息出现顺序去重）
		seenConvCh := map[string]bool{}
		appendConvCh := func(chID string) {
			if chID == "" || seenConvCh[chID] {
				return
			}
			if s, ok := channels[chID]; ok {
				seenConvCh[chID] = true
				d.Channels = append(d.Channels, s)
			}
		}
		if c.ChannelID != nil {
			appendConvCh(*c.ChannelID)
		}
		for _, chID := range convChannelIDs[c.ID] {
			appendConvCh(chID)
		}
		if c.ReplyChannelID != nil {
			appendConvCh(*c.ReplyChannelID)
		}
		if c.ExternalGroupID != nil {
			if s, ok := extGroups[*c.ExternalGroupID]; ok {
				d.ExternalGroup = &s
				if s.Language != "" {
					d.Language = s.Language
				}
			}
		} else if c.ContactID != nil {
			s := contacts[*c.ContactID]
			d.Contact = &s
			// 有联系人时，客户的语言以联系人为准，同一联系人的多个会话始终一致
			d.Language = s.Language
			targetAudience := c.Audience
			if targetAudience == "" {
				targetAudience = channel.AudienceCustomer
			}
			for _, chID := range availableByContact[*c.ContactID] {
				if chSummary, ok := channels[chID]; ok && chSummary.Audience == targetAudience {
					d.AvailableChannels = append(d.AvailableChannels, chSummary)
				}
			}
		}
		if c.AssigneeType != "" {
			d.Assignee = &Actor{Type: c.AssigneeType, ID: c.AssigneeID}
		}
		out[i] = d
	}
	return out, nil
}

// previewText 返回会话列表中最后一条消息 it 给成员 viewerID（语言 language）看的文字：
// 非本人发出的可译消息若有对应语言的译文则显示译文，其余回退到 SourceText（成员翻译发出的回复显示原文）。
func previewText(it *Item, viewerID, language string) string {
	isOwn := it.ActorType == MemberActor && viewerID != "" && it.ActorID == viewerID
	if !isOwn && language != "" && it.Translatable() {
		if translations, _ := it.Derived["translations"].(map[string]any); translations != nil {
			if t, _ := translations[language].(string); t != "" {
				return t
			}
		}
	}
	return it.SourceText()
}
