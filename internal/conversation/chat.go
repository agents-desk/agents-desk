package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/account"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/member"
)

// ChatMode 是聊天的模式：单聊或群聊。
type ChatMode string

const (
	ChatDirect ChatMode = "direct" // 单聊：两名参与者之间唯一的一条长期会话
	ChatGroup  ChatMode = "group"  // 群聊：带名称的多人协作会话或外部群聊
)

// ChatModes 是聊天的全部模式，显示文字是翻译文件中 chat.mode.<模式> 的消息。
var ChatModes = enum.New("chat.mode", enum.Item(ChatDirect), enum.Item(ChatGroup))

// Schema 让 OpenAPI 中的 ChatMode 只接受目录中的取值。
func (ChatMode) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, ChatModes) }

// ChatScope 是聊天的范围：工作区内部聊天或经个人商务渠道开展的外部商务沟通。
type ChatScope string

const (
	ChatInternal ChatScope = "internal" // 工作区内部聊天（同事单聊、内部群聊及未来内部 AI 助手）
	ChatExternal ChatScope = "external" // 外部商务沟通（经个人网页沟通链接、Telegram 个人号等接入）
)

// ChatScopes 是聊天的全部范围，显示文字是翻译文件中 chat.scope.<范围> 的消息。
var ChatScopes = enum.New("chat.scope", enum.Item(ChatInternal), enum.Item(ChatExternal))

// Schema 让 OpenAPI 中的 ChatScope 只接受目录中的取值。
func (ChatScope) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, ChatScopes) }

// ChatMeta 是 chats 扩展表的数据库模型，与 conversations（kind=chat）一对一关联。
type ChatMeta struct {
	bun.BaseModel `bun:"table:chats"`

	ConversationID string `bun:"conversation_id,pk"`
	database.Timestamps
	TenantID       string    `bun:"tenant_id,notnull"`
	Mode           ChatMode  `bun:"mode,notnull"`
	Scope          ChatScope `bun:"scope,notnull"`
	Name           string    `bun:"name,notnull"`
	DMKey          *string   `bun:"dm_key"`
	ChannelID      *string   `bun:"channel_id"`
	ExternalChatID string    `bun:"external_chat_id,notnull"`
	CreatedBy      *string   `bun:"created_by"`
}

// ChatParticipantDTO 是聊天参与者视图（支持工作区成员、外部联系人及未来机器人/AI Agent）。
type ChatParticipantDTO struct {
	Type      ActorType `json:"type"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Language  string    `json:"language,omitempty" doc:"参与者偏好的翻译语言（BCP 47）"`
	Status    string    `json:"status,omitempty" doc:"在线状态：online 或 away"`
	JoinedAt  time.Time `json:"joined_at"`
}

// ChatDTO 是接口中的聊天视图，聚合了会话内核、范围与商务渠道信息、参与者与未读状态。
type ChatDTO struct {
	ID                  string               `json:"id"`
	Mode                ChatMode             `json:"mode" doc:"direct（单聊）或 group（群聊）"`
	Scope               ChatScope            `json:"scope" doc:"internal（内部聊天）或 external（商务沟通）"`
	Name                string               `json:"name" doc:"群聊名称；单聊为空，前端展示 peer.name"`
	Channel             *channel.Summary     `json:"channel,omitempty" doc:"外部商务聊天所属的个人商务渠道"`
	Contact             *contact.Summary     `json:"contact,omitempty" doc:"外部 1:1 商务聊天的对方联系人概要"`
	Language            string               `json:"language,omitempty" doc:"外部商务聊天识别或手动设置的对方语言（BCP 47）"`
	ExternalChatID      string               `json:"external_chat_id,omitempty" doc:"外部渠道中的会话或群聊唯一标识"`
	Peer                *ChatParticipantDTO  `json:"peer,omitempty" doc:"单聊时的对方参与者资料"`
	ParticipantCount    int                  `json:"participant_count" doc:"当前聊天参与者总数"`
	Participants        []ChatParticipantDTO `json:"participants,omitempty" doc:"聊天参与者列表（详情接口返回）"`
	Preview             string               `json:"preview" doc:"最近一条消息的文字摘要（非本人消息优先显示我的语言译文）"`
	PreviewActor        *Actor               `json:"preview_actor,omitempty"`
	PreviewAttachment   *PreviewAttachment   `json:"preview_attachment,omitempty"`
	PreviewItemID       string               `json:"preview_item_id,omitempty"`
	PreviewUntranslated bool                 `json:"preview_untranslated,omitempty"`
	Match               *SearchMatch         `json:"match,omitempty" doc:"按关键词搜索聊天记录时命中的代表性消息与高亮摘要"`
	MatchCount          int                  `json:"match_count,omitempty" doc:"按关键词搜索时该聊天中命中的消息总条数"`
	LastMessageAt       time.Time            `json:"last_message_at"`
	LastSeq             int64                `json:"last_seq"`
	UnreadCount         int                  `json:"unread_count" doc:"未读消息条数"`
	Unread              bool                 `json:"unread"`
	ReadSeq             int64                `json:"read_seq"`
	CreatedBy           *string              `json:"created_by,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

// ChatOutput 是返回单个聊天的响应。
type ChatOutput struct {
	Body ChatDTO
}

// buildDMKey 把两个参与者按 type:id 字典序排序后拼成确定的单聊唯一键，支持 member 与未来 bot。
func buildDMKey(aType ActorType, aID string, bType ActorType, bID string) string {
	left := string(aType) + ":" + aID
	right := string(bType) + ":" + bID
	if left > right {
		left, right = right, left
	}
	return left + "|" + right
}

// findVisibleChat 读取 sub 在其工作区中参与的聊天 id（同时加载 conversations 与 chats）；
// 不存在、不是聊天型会话或 sub 不是参与者时返回 ErrNotFound。lock 为 true 时按 conversations -> chats 顺序加行锁。
func findVisibleChat(ctx context.Context, db bun.IDB, sub identity.Subject, id string, lock bool) (*Conversation, *ChatMeta, error) {
	c, err := findVisible(ctx, db, sub, id, lock)
	if err != nil {
		return nil, nil, err
	}
	if c.Kind != Chat {
		return nil, nil, ErrNotFound.New()
	}
	cm := new(ChatMeta)
	q := db.NewSelect().Model(cm).Where("conversation_id = ? AND tenant_id = ?", c.ID, c.TenantID)
	if lock {
		q = q.For("UPDATE")
	}
	err = q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound.New()
	}
	return c, cm, errors.Wrap(err, "load chat meta")
}

// toChatDTOs 批量把聊天型会话转为 ChatDTO；includeParticipants 为 true 时填充完整参与者列表。
func toChatDTOs(ctx context.Context, db bun.IDB, files *filesystem.Service, sub identity.Subject, convs []Conversation, includeParticipants bool) ([]ChatDTO, error) {
	out := make([]ChatDTO, len(convs))
	if len(convs) == 0 {
		return out, nil
	}
	ids := make([]string, len(convs))
	channelIDSet := map[string]struct{}{}
	channelIDs := make([]string, 0, len(convs))
	contactIDSet := map[string]struct{}{}
	contactIDs := make([]string, 0, len(convs))
	addCh := func(id *string) {
		if id == nil || *id == "" {
			return
		}
		if _, ok := channelIDSet[*id]; !ok {
			channelIDSet[*id] = struct{}{}
			channelIDs = append(channelIDs, *id)
		}
	}
	addContact := func(id string) {
		if id == "" {
			return
		}
		if _, ok := contactIDSet[id]; !ok {
			contactIDSet[id] = struct{}{}
			contactIDs = append(contactIDs, id)
		}
	}
	for i := range convs {
		ids[i] = convs[i].ID
		addCh(convs[i].ChannelID)
		if convs[i].ContactID != nil {
			addContact(*convs[i].ContactID)
		}
	}

	var metas []ChatMeta
	if err := db.NewSelect().Model(&metas).Where("tenant_id = ? AND conversation_id IN (?)", sub.Tenant, bun.In(ids)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load chat metas")
	}
	metaByID := make(map[string]ChatMeta, len(metas))
	for _, m := range metas {
		metaByID[m.ConversationID] = m
		addCh(m.ChannelID)
	}

	var parts []Participant
	if err := db.NewSelect().Model(&parts).
		Where("conversation_id IN (?)", bun.In(ids)).
		OrderExpr("created_at ASC, participant_id ASC").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load chat participants")
	}
	partsByConv := make(map[string][]Participant, len(ids))
	memberIDSet := map[string]struct{}{}
	memberIDs := make([]string, 0, len(parts))
	for _, p := range parts {
		partsByConv[p.ConversationID] = append(partsByConv[p.ConversationID], p)
		switch p.Type {
		case MemberActor:
			if _, ok := memberIDSet[p.ID]; !ok {
				memberIDSet[p.ID] = struct{}{}
				memberIDs = append(memberIDs, p.ID)
			}
		case ContactActor:
			addContact(p.ID)
		}
	}

	// 加载参与成员的姓名、头像和在线状态
	memberProfile := map[string]ChatParticipantDTO{}
	if len(memberIDs) > 0 {
		var mRows []member.Member
		if err := db.NewSelect().Model(&mRows).Where("id IN (?)", bun.In(memberIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load chat member profiles")
		}
		statuses, err := member.StatusesOf(ctx, db, sub.Tenant, memberIDs)
		if err != nil {
			return nil, err
		}
		for i := range mRows {
			m := &mRows[i]
			var avatar string
			if files != nil && m.AvatarFileID != nil {
				var err error
				avatar, err = account.AvatarURL(ctx, files, m.AvatarFileID)
				if err != nil {
					return nil, err
				}
			}
			memberProfile[m.ID] = ChatParticipantDTO{
				Type:      MemberActor,
				ID:        m.ID,
				Name:      m.Name,
				AvatarURL: avatar,
				Language:  string(m.Language),
				Status:    string(statuses[m.ID]),
			}
		}
	}

	// 加载外部商务聊天关联的渠道与联系人概要
	channels := map[string]channel.Summary{}
	if len(channelIDs) > 0 {
		var chRows []channel.Channel
		if err := db.NewSelect().Model(&chRows).Where("id IN (?)", bun.In(channelIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load chat channels")
		}
		for i := range chRows {
			channels[chRows[i].ID] = chRows[i].Summary()
		}
	}
	contactSummary := map[string]contact.Summary{}
	if len(contactIDs) > 0 {
		var ctRows []contact.Contact
		if err := db.NewSelect().Model(&ctRows).Where("id IN (?)", bun.In(contactIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load chat contacts")
		}
		for i := range ctRows {
			contactSummary[ctRows[i].ID] = ctRows[i].Summary()
		}
	}

	// 加载最近一条消息作为预览（支持跨语言译文与未译标记）
	language, err := MemberLanguage(ctx, db, sub)
	if err != nil {
		return nil, err
	}
	var lasts []Item
	err = db.NewRaw(`SELECT DISTINCT ON (conversation_id) * FROM timeline_items
		WHERE conversation_id IN (?) AND kind = ? ORDER BY conversation_id, (actor_type = ?), seq DESC`,
		bun.In(ids), Message, SystemActor).Scan(ctx, &lasts)
	if err != nil {
		return nil, errors.Wrap(err, "load chat previews")
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
	readSeq, err := readSeqOf(ctx, db, sub, ids)
	if err != nil {
		return nil, err
	}
	unreadCounts, err := chatUnreadCounts(ctx, db, sub, ids)
	if err != nil {
		return nil, err
	}

	toPartDTO := func(p Participant) ChatParticipantDTO {
		switch p.Type {
		case MemberActor:
			if prof, ok := memberProfile[p.ID]; ok {
				prof.JoinedAt = p.CreatedAt
				return prof
			}
		case ContactActor:
			if ct, ok := contactSummary[p.ID]; ok {
				return ChatParticipantDTO{
					Type:     ContactActor,
					ID:       ct.ID,
					Name:     ct.Name,
					Language: ct.Language,
					JoinedAt: p.CreatedAt,
				}
			}
		}
		return ChatParticipantDTO{Type: p.Type, ID: p.ID, JoinedAt: p.CreatedAt}
	}

	for i, c := range convs {
		m := metaByID[c.ID]
		scope := m.Scope
		if scope == "" {
			scope = ChatInternal
		}
		convParts := partsByConv[c.ID]
		uCount := unreadCounts[c.ID]
		dto := ChatDTO{
			ID:               c.ID,
			Mode:             m.Mode,
			Scope:            scope,
			Name:             m.Name,
			Language:         c.Language,
			ExternalChatID:   m.ExternalChatID,
			ParticipantCount: len(convParts),
			LastMessageAt:    c.LastMessageAt,
			LastSeq:          c.LastSeq,
			UnreadCount:      uCount,
			Unread:           uCount > 0,
			ReadSeq:          readSeq[c.ID],
			CreatedBy:        m.CreatedBy,
			CreatedAt:        c.CreatedAt,
			UpdatedAt:        c.UpdatedAt,
		}
		chID := m.ChannelID
		if chID == nil {
			chID = c.ChannelID
		}
		if chID != nil {
			if s, ok := channels[*chID]; ok {
				dto.Channel = &s
			}
		}
		if c.ContactID != nil {
			if s, ok := contactSummary[*c.ContactID]; ok {
				dto.Contact = &s
				if s.Language != "" {
					dto.Language = s.Language
				}
			}
		}
		if m.Mode == ChatDirect {
			// 外部单聊优先以联系人作为对方；内部单聊以非当前成员作为对方
			for _, p := range convParts {
				if scope == ChatExternal && p.Type == ContactActor {
					peer := toPartDTO(p)
					dto.Peer = &peer
					break
				}
				if scope == ChatInternal && !(p.Type == MemberActor && p.ID == sub.ID) {
					peer := toPartDTO(p)
					dto.Peer = &peer
					break
				}
			}
			if dto.Peer == nil && c.ContactID != nil {
				if ct, ok := contactSummary[*c.ContactID]; ok {
					dto.Peer = &ChatParticipantDTO{Type: ContactActor, ID: ct.ID, Name: ct.Name, Language: ct.Language}
				}
			}
		}
		if includeParticipants {
			list := make([]ChatParticipantDTO, 0, len(convParts))
			for _, p := range convParts {
				list = append(list, toPartDTO(p))
			}
			dto.Participants = list
		}
		if p := previews[c.ID]; p != nil {
			dto.Preview = previewText(p, sub.ID, language)
			actor := Actor{Type: p.ActorType, ID: p.ActorID}
			if p.ActorType == ContactActor {
				if ct, ok := contactSummary[p.ActorID]; ok {
					actor.Name = ct.Name
				}
			}
			dto.PreviewActor = &actor
			dto.PreviewAttachment = previewAttachments[p.ID]
			if untranslated(p, sub.ID, language) {
				dto.PreviewItemID = p.ID
				dto.PreviewUntranslated = true
			}
		}
		out[i] = dto
	}
	return out, nil
}

// chatUnreadCounts 返回成员 sub 在聊天 ids 中各自未读的消息条数（排除系统事件和本人发出的消息）。
func chatUnreadCounts(ctx context.Context, db bun.IDB, sub identity.Subject, ids []string) (map[string]int, error) {
	out := map[string]int{}
	if sub.Type != member.Type || len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ConversationID string `bun:"conversation_id"`
		Count          int    `bun:"cnt"`
	}
	err := db.NewRaw(`SELECT t.conversation_id, count(*) AS cnt
		FROM timeline_items t
		LEFT JOIN conversation_reads r ON r.conversation_id = t.conversation_id AND r.member_id = ?
		WHERE t.tenant_id = ? AND t.conversation_id IN (?) AND t.kind IN (?, ?) AND t.actor_type <> ?
			AND NOT (t.actor_type = ? AND t.actor_id = ?)
			AND t.seq > coalesce(r.last_read_seq, 0)
		GROUP BY t.conversation_id`,
		sub.ID, sub.Tenant, bun.In(ids), Message, Note, SystemActor, MemberActor, sub.ID).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "count chat unread messages")
	}
	for _, r := range rows {
		out[r.ConversationID] = r.Count
	}
	return out, nil
}
