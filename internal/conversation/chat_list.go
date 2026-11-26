package conversation

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// ListChatsInput 是内部聊天列表的查询参数。
type ListChatsInput struct {
	page.CursorParams
	Mode ChatMode `query:"mode" doc:"可选：按聊天模式筛选（direct 或 group）"`
	Q    string   `query:"q" maxLength:"100" doc:"按群聊名称或单聊对方姓名搜索"`
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

	if in.Mode != "" {
		q = q.Where("ch.mode = ?", in.Mode)
	}
	if kw := strings.TrimSpace(in.Q); kw != "" {
		like := "%" + kw + "%"
		q = q.Where(`(
			(ch.mode = ? AND ch.name ILIKE ?)
			OR (ch.mode = ? AND EXISTS (
				SELECT 1 FROM conversation_participants cp
				JOIN members m ON m.id = cp.participant_id
				WHERE cp.conversation_id = conversation.id AND cp.participant_type = ? AND cp.participant_id <> ?
					AND m.name ILIKE ?
			))
		)`, ChatGroup, like, ChatDirect, MemberActor, sub.ID, like)
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

// ChatUnreadDTO 是成员在工作区内的内部聊天未读汇总，用于图标栏角标。
type ChatUnreadDTO struct {
	UnreadChats    int `json:"unread_chats" doc:"包含未读消息的聊天会话数"`
	UnreadMessages int `json:"unread_messages" doc:"未读消息总条数"`
}

// ChatUnreadOutput 是内部聊天未读汇总响应。
type ChatUnreadOutput struct {
	Body ChatUnreadDTO
}

// GetChatUnread 统计当前成员在工作区内的内部聊天未读数。
type GetChatUnread struct {
	db *bun.DB
}

// NewGetChatUnread 返回内部聊天未读统计动作。
func NewGetChatUnread(db *bun.DB) *GetChatUnread { return &GetChatUnread{db: db} }

// Handle 返回成员 sub 参与的内部聊天中的未读会话数和未读消息总数。
func (a *GetChatUnread) Handle(ctx context.Context, sub identity.Subject) (*ChatUnreadDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if sub.Type != member.Type {
		return &ChatUnreadDTO{}, nil
	}
	var out ChatUnreadDTO
	err := a.db.NewRaw(`SELECT count(DISTINCT t.conversation_id) AS unread_chats, count(*) AS unread_messages
		FROM timeline_items t
		JOIN conversations c ON c.id = t.conversation_id
		JOIN conversation_participants cp ON cp.conversation_id = c.id AND cp.participant_type = ? AND cp.participant_id = ?
		LEFT JOIN conversation_reads r ON r.conversation_id = c.id AND r.member_id = ?
		WHERE c.tenant_id = ? AND c.kind = ? AND t.kind = ? AND t.actor_type <> ?
			AND NOT (t.actor_type = ? AND t.actor_id = ?)
			AND t.seq > coalesce(r.last_read_seq, 0)`,
		MemberActor, sub.ID, sub.ID, sub.Tenant, Chat, Message, SystemActor, MemberActor, sub.ID).
		Scan(ctx, &out.UnreadChats, &out.UnreadMessages)
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
