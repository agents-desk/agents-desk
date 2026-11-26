package conversation

import (
	"context"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/auth"
	"agents-desk/internal/tickettype"
)

// ListTicketsInput 是工单列表的查询参数。
type ListTicketsInput struct {
	page.CursorParams
	Inbox          Inbox                 `query:"inbox" default:"all" doc:"工单列表分组：mine（指派给我）、unassigned（未指派）或 all（全部）"`
	TeamID         string                `query:"team_id" format:"uuid" doc:"按团队收件箱筛选"`
	Status         TicketStatus          `query:"status" doc:"按单个工单状态筛选"`
	UnresolvedOnly bool                  `query:"unresolved_only" doc:"为 true 时只列出未解决的工单（排除 resolved）"`
	TypeID         string                `query:"type_id" format:"uuid" doc:"按工单类型筛选"`
	Visibility     tickettype.Visibility `query:"visibility" doc:"按可见范围筛选（all 或 members）"`
	Priority       Priority              `query:"priority" doc:"按优先级筛选"`
	ConversationID string                `query:"conversation_id" format:"uuid" doc:"只列出关联了该服务型会话的工单"`
	ContactID      string                `query:"contact_id" format:"uuid" doc:"只列出关联了该联系人任意会话的工单"`
	Q              string                `query:"q" maxLength:"100" doc:"按工单编号（如 #1024 或 1024）或标题关键词搜索"`
}

// ListTicketsOutput 是一页工单。
type ListTicketsOutput struct {
	Body page.Cursor[TicketDTO]
}

// ListTickets 列出当前成员可见的工单，支持游标分页和多维度筛选。
type ListTickets struct {
	db *bun.DB
}

// NewListTickets 返回工单列表的动作。
func NewListTickets(db *bun.DB) *ListTickets { return &ListTickets{db: db} }

// Handle 返回 sub 所在工作区中满足筛选条件的一页可见工单，按最近活动时间倒序排列。
func (a *ListTickets) Handle(ctx context.Context, sub identity.Subject, in *ListTicketsInput) (*page.Cursor[TicketDTO], error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if in == nil {
		in = &ListTicketsInput{}
	}

	var rows []Conversation
	q := a.db.NewSelect().Model(&rows).
		Join("JOIN tickets tk ON tk.conversation_id = conversation.id").
		Where("conversation.tenant_id = ? AND conversation.kind = ?", sub.Tenant, Ticket).
		Limit(in.Limit())
	q = visible(q, sub)

	switch in.Inbox {
	case InboxMine:
		q = q.Where("conversation.assignee_type = ? AND conversation.assignee_id = ?", MemberActor, sub.ID)
	case InboxUnassigned:
		q = q.Where("conversation.team_id IS NULL AND conversation.assignee_type = ''")
	}
	if in.TeamID != "" {
		q = q.Where("conversation.team_id = ?", in.TeamID)
	}
	if in.Status != "" {
		q = q.Where("conversation.status = ?", in.Status)
	} else if in.UnresolvedOnly {
		q = q.Where("conversation.status <> ?", TicketResolved)
	}
	if in.TypeID != "" {
		q = q.Where("tk.type_id = ?", in.TypeID)
	}
	if in.Visibility != "" {
		q = q.Where("tk.visibility = ?", in.Visibility)
	}
	if in.Priority != "" {
		q = q.Where("conversation.priority = ?", in.Priority)
	}
	if in.ConversationID != "" {
		q = q.Where("EXISTS (SELECT 1 FROM conversation_links cl WHERE cl.tenant_id = ? AND cl.ticket_id = conversation.id AND cl.conversation_id = ?)", sub.Tenant, in.ConversationID)
	}
	if in.ContactID != "" {
		q = q.Where(`EXISTS (
			SELECT 1 FROM conversation_links cl
			JOIN conversations sc ON sc.id = cl.conversation_id
			WHERE cl.tenant_id = ? AND cl.ticket_id = conversation.id AND sc.tenant_id = ? AND sc.kind = ? AND sc.contact_id = ?
		)`, sub.Tenant, sub.Tenant, Service, in.ContactID)
	}
	if kw := strings.TrimSpace(in.Q); kw != "" {
		numStr := strings.TrimPrefix(kw, "#")
		if num, err := strconv.ParseInt(numStr, 10, 64); err == nil && num > 0 {
			q = q.Where("(tk.number = ? OR tk.title ILIKE ?)", num, "%"+kw+"%")
		} else {
			q = q.Where("tk.title ILIKE ?", "%"+kw+"%")
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
		return nil, errors.Wrap(err, "list tickets")
	}
	items, err := toTicketDTOs(ctx, a.db, sub, rows, false)
	if err != nil {
		return nil, err
	}
	p := page.NewCursor(items, in.CursorParams, func(d TicketDTO) any {
		return timeKey{At: d.LastMessageAt, ID: d.ID}
	})
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/tickets。
func (a *ListTickets) HTTP(ctx context.Context, in *ListTicketsInput) (*ListTicketsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListTicketsOutput{Body: *p}, nil
}
