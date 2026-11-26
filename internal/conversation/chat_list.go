package conversation

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/search"
	"github.com/go-tenon/tenon/search/gse"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// ListChatsInput 是聊天列表的查询参数。
type ListChatsInput struct {
	page.CursorParams
	Scope     ChatScope `query:"scope" doc:"可选：按聊天范围筛选（internal 内部聊天，external 商务沟通）"`
	Mode      ChatMode  `query:"mode" doc:"可选：按聊天模式筛选（direct 或 group）"`
	ChannelID string    `query:"channel_id" format:"uuid" doc:"可选：按所属商务渠道 ID 筛选"`
	MemberID  string    `query:"member_id" doc:"可选：只返回同时包含指定成员的聊天（用于查询共同群聊）"`
	Q         string    `query:"q" maxLength:"100" doc:"按群聊名称、参与者姓名、联系人姓名或聊天消息全文搜索"`
}

// ListChatsOutput 是一页内部聊天列表。
type ListChatsOutput struct {
	Body page.Cursor[ChatDTO]
}

// ListChats 列出当前成员参与的内部聊天（单聊与群聊），按最近活动时间倒序排列。
type ListChats struct {
	db    *bun.DB
	files *filesystem.Service
}

// NewListChats 返回内部聊天列表的动作。
func NewListChats(db *bun.DB, files ...*filesystem.Service) *ListChats {
	var f *filesystem.Service
	if len(files) > 0 {
		f = files[0]
	}
	return &ListChats{db: db, files: f}
}

// Handle 返回 sub 在当前工作区参与的一页内部聊天。
func (a *ListChats) Handle(ctx context.Context, sub identity.Subject, in *ListChatsInput) (*page.Cursor[ChatDTO], error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if in == nil {
		in = &ListChatsInput{}
	}

	var rows []Conversation
	q := a.db.NewSelect().Model(&rows).
		Join("JOIN chats ch ON ch.conversation_id = conversation.id").
		Where("conversation.tenant_id = ? AND conversation.kind = ?", sub.Tenant, Chat).
		Limit(in.Limit())
	q = visible(q, sub)

	if in.Scope != "" {
		q = q.Where("ch.scope = ?", in.Scope)
	}
	if in.Mode != "" {
		q = q.Where("ch.mode = ?", in.Mode)
	}
	if chID := strings.TrimSpace(in.ChannelID); chID != "" {
		q = q.Where("ch.channel_id = ?", chID)
	}
	if memberID := strings.TrimSpace(in.MemberID); memberID != "" {
		q = q.Where(`EXISTS (
			SELECT 1 FROM conversation_participants cp_member
			WHERE cp_member.conversation_id = conversation.id
				AND cp_member.participant_type = ? AND cp_member.participant_id = ?
		)`, MemberActor, memberID)
	}
	var matchByChat map[string]SearchMatch
	var matchCountByChat map[string]int
	if kw := strings.TrimSpace(in.Q); kw != "" {
		var matchedChatIDs []string
		// 结合 SearchIndex 全文检索当前成员可见的聊天消息与备注（原文与译文），按会话折叠并返回高亮摘要
		if len(gse.Tokenizer.Query(kw)) > 0 {
			allowed := a.db.NewSelect().Model((*Conversation)(nil)).ColumnExpr("conversation.id::text").
				Join("JOIN chats ch ON ch.conversation_id = conversation.id").
				Where("conversation.tenant_id = ? AND conversation.kind = ?", sub.Tenant, Chat)
			allowed = visible(allowed, sub)
			if in.Scope != "" {
				allowed = allowed.Where("ch.scope = ?", in.Scope)
			}
			if in.Mode != "" {
				allowed = allowed.Where("ch.mode = ?", in.Mode)
			}
			if chID := strings.TrimSpace(in.ChannelID); chID != "" {
				allowed = allowed.Where("ch.channel_id = ?", chID)
			}
			res, err := SearchIndex.Search(ctx, a.db, search.Query{
				Text:     kw,
				Tenant:   sub.Tenant,
				Filter:   search.InQuery("conversation_id", allowed),
				Sort:     []search.Sort{search.Relevance(), search.Desc("created_at")},
				Collapse: "conversation_id",
				Page:     page.OffsetParams{Page: 1, PerPage: 100},
			})
			if err != nil {
				return nil, errors.Wrap(err, "search chat messages")
			}
			if len(res.Items) > 0 {
				matchByChat = make(map[string]SearchMatch, len(res.Items))
				matchCountByChat = make(map[string]int, len(res.Items))
				matchedChatIDs = make([]string, 0, len(res.Items))
				for _, hit := range res.Items {
					field, snippet := pickSearchSnippet(hit)
					count := hit.GroupCount
					if count < 1 {
						count = 1
					}
					matchedChatIDs = append(matchedChatIDs, hit.Doc.ConversationID)
					matchByChat[hit.Doc.ConversationID] = SearchMatch{
						ItemID:    hit.Key,
						Seq:       hit.Doc.Seq,
						Kind:      hit.Doc.Kind,
						Actor:     Actor{Type: hit.Doc.ActorType, ID: hit.Doc.ActorID},
						Field:     field,
						Snippet:   snippet,
						CreatedAt: hit.Doc.CreatedAt,
					}
					matchCountByChat[hit.Doc.ConversationID] = count
				}
			}
		}

		like := "%" + kw + "%"
		// 统一支持按群聊名、参与者成员名、外部联系人名，以及全文命中聊天记录筛选
		if len(matchedChatIDs) > 0 {
			q = q.Where(`(
				(ch.mode = ? AND ch.name ILIKE ?)
				OR EXISTS (
					SELECT 1 FROM conversation_participants cp
					JOIN members m ON m.id::text = cp.participant_id
					WHERE cp.conversation_id = conversation.id AND cp.participant_type = ? AND cp.participant_id <> ?
						AND m.name ILIKE ?
				)
				OR EXISTS (
					SELECT 1 FROM contacts ct
					WHERE ct.id = conversation.contact_id AND ct.tenant_id = conversation.tenant_id
						AND (ct.name ILIKE ? OR ct.email ILIKE ?)
				)
				OR conversation.id IN (?)
			)`, ChatGroup, like, MemberActor, sub.ID, like, like, like, bun.In(matchedChatIDs))
		} else {
			q = q.Where(`(
				(ch.mode = ? AND ch.name ILIKE ?)
				OR EXISTS (
					SELECT 1 FROM conversation_participants cp
					JOIN members m ON m.id::text = cp.participant_id
					WHERE cp.conversation_id = conversation.id AND cp.participant_type = ? AND cp.participant_id <> ?
						AND m.name ILIKE ?
				)
				OR EXISTS (
					SELECT 1 FROM contacts ct
					WHERE ct.id = conversation.contact_id AND ct.tenant_id = conversation.tenant_id
						AND (ct.name ILIKE ? OR ct.email ILIKE ?)
				)
			)`, ChatGroup, like, MemberActor, sub.ID, like, like, like)
		}
	}

	q = q.OrderExpr("conversation.last_message_at DESC, conversation.id DESC")
	var after timeKey
	if ok, err := in.After(&after); err != nil {
		return nil, err
	} else if ok {
		q = q.Where("(conversation.last_message_at, conversation.id) < (?, ?)", after.At, after.ID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list chats")
	}
	items, err := toChatDTOs(ctx, a.db, a.files, sub, rows, false)
	if err != nil {
		return nil, err
	}
	if len(matchByChat) > 0 {
		for i := range items {
			if m, ok := matchByChat[items[i].ID]; ok {
				matchCopy := m
				items[i].Match = &matchCopy
				items[i].MatchCount = matchCountByChat[items[i].ID]
			}
		}
	}
	p := page.NewCursor(items, in.CursorParams, func(d ChatDTO) any {
		return timeKey{At: d.LastMessageAt, ID: d.ID}
	})
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/chats。
func (a *ListChats) HTTP(ctx context.Context, in *ListChatsInput) (*ListChatsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListChatsOutput{Body: *p}, nil
}

// GetChat 读取单条内部聊天的详情与参与者列表。
type GetChat struct {
	db    *bun.DB
	files *filesystem.Service
}

// NewGetChat 返回读取单条内部聊天的动作。
func NewGetChat(db *bun.DB, files ...*filesystem.Service) *GetChat {
	var f *filesystem.Service
	if len(files) > 0 {
		f = files[0]
	}
	return &GetChat{db: db, files: f}
}

// Handle 返回 sub 参与的内部聊天 id 的详情与完整参与者列表；非参与者或不存在返回 ErrNotFound。
func (a *GetChat) Handle(ctx context.Context, sub identity.Subject, id string) (*ChatDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	c, _, err := findVisibleChat(ctx, a.db, sub, id, false)
	if err != nil {
		return nil, err
	}
	dtos, err := toChatDTOs(ctx, a.db, a.files, sub, []Conversation{*c}, true)
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 GET /t/{slug}/api/chats/{id}。
func (a *GetChat) HTTP(ctx context.Context, in *IDInput) (*ChatOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ChatOutput{Body: *dto}, nil
}

// ChatUnreadDTO 是成员在工作区内的聊天未读汇总，用于图标栏角标及二级导航的内部聊天/商务沟通未读数。
type ChatUnreadDTO struct {
	UnreadChats            int `json:"unread_chats" doc:"包含未读消息的聊天会话总数"`
	UnreadMessages         int `json:"unread_messages" doc:"未读消息总条数"`
	InternalUnreadMessages int `json:"internal_unread_messages" doc:"内部聊天未读消息条数"`
	ExternalUnreadMessages int `json:"external_unread_messages" doc:"商务沟通未读消息条数"`
}

// ChatUnreadOutput 是聊天未读汇总响应。
type ChatUnreadOutput struct {
	Body ChatUnreadDTO
}

// GetChatUnread 统计当前成员在工作区内的聊天未读数。
type GetChatUnread struct {
	db *bun.DB
}

// NewGetChatUnread 返回聊天未读统计动作。
func NewGetChatUnread(db *bun.DB) *GetChatUnread { return &GetChatUnread{db: db} }

// Handle 返回成员 sub 参与的聊天中的未读会话数和未读消息总数（含内部与商务分组统计）。
func (a *GetChatUnread) Handle(ctx context.Context, sub identity.Subject) (*ChatUnreadDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if sub.Type != member.Type {
		return &ChatUnreadDTO{}, nil
	}
	var out ChatUnreadDTO
	err := a.db.NewRaw(`SELECT
			count(DISTINCT t.conversation_id) AS unread_chats,
			count(*) AS unread_messages,
			count(*) FILTER (WHERE ch.scope = ?) AS internal_unread_messages,
			count(*) FILTER (WHERE ch.scope = ?) AS external_unread_messages
		FROM timeline_items t
		JOIN conversations c ON c.id = t.conversation_id
		JOIN chats ch ON ch.conversation_id = c.id
		JOIN conversation_participants cp ON cp.conversation_id = c.id AND cp.participant_type = ? AND cp.participant_id = ?
		LEFT JOIN conversation_reads r ON r.conversation_id = c.id AND r.member_id = ?
		WHERE c.tenant_id = ? AND c.kind = ? AND t.kind IN (?, ?) AND t.actor_type <> ?
			AND NOT (t.actor_type = ? AND t.actor_id = ?)
			AND t.seq > coalesce(r.last_read_seq, 0)`,
		ChatInternal, ChatExternal, MemberActor, sub.ID, sub.ID, sub.Tenant, Chat, Message, Note, SystemActor, MemberActor, sub.ID).
		Scan(ctx, &out.UnreadChats, &out.UnreadMessages, &out.InternalUnreadMessages, &out.ExternalUnreadMessages)
	if err != nil {
		return nil, errors.Wrap(err, "count chat unread summary")
	}
	return &out, nil
}

// HTTP 处理 GET /t/{slug}/api/chats/unread。
func (a *GetChatUnread) HTTP(ctx context.Context, _ *struct{}) (*ChatUnreadOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &ChatUnreadOutput{Body: *dto}, nil
}
