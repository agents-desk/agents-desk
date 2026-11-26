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
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/tag"
	"agents-desk/internal/tickettype"
)

// LinkRelation 是工单与服务型会话的关联来源。
type LinkRelation string

const (
	LinkSource LinkRelation = "source" // 由该会话转成工单
	LinkLinked LinkRelation = "linked" // 手动关联到已有工单
)

// LinkRelations 是全部关联来源类型。
var LinkRelations = enum.New("ticket.link_relation", enum.Item(LinkSource), enum.Item(LinkLinked))

// Schema 让 OpenAPI 中的 LinkRelation 只接受目录中的取值。
func (LinkRelation) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, LinkRelations)
}

// TicketMeta 是 tickets 扩展表的数据库模型，与 conversations（kind=ticket）一对一关联。
type TicketMeta struct {
	bun.BaseModel `bun:"table:tickets"`

	ConversationID string `bun:"conversation_id,pk"`
	database.Timestamps
	TenantID    string                `bun:"tenant_id,notnull"`
	Number      int64                 `bun:"number,notnull"`
	TypeID      string                `bun:"type_id,notnull"`
	Title       string                `bun:"title,notnull"`
	Description string                `bun:"description,notnull"`
	Visibility  tickettype.Visibility `bun:"visibility,notnull"`
	CreatedBy   *string               `bun:"created_by"`
	ResolvedAt  *time.Time            `bun:"resolved_at"`
}

// ConversationLink 是 conversation_links 关联表的数据库模型，多对多连接工单与服务型会话。
type ConversationLink struct {
	bun.BaseModel `bun:"table:conversation_links"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID       string       `bun:"tenant_id,notnull"`
	TicketID       string       `bun:"ticket_id,notnull"`
	ConversationID string       `bun:"conversation_id,notnull"`
	Relation       LinkRelation `bun:"relation,notnull"`
	CreatedBy      *string      `bun:"created_by"`
}

// LinkedConversationDTO 是工单详情中展示的一条已关联服务型会话摘要（仅返回当前成员有权限看到的会话）。
type LinkedConversationDTO struct {
	ID            string           `json:"id"`
	Relation      LinkRelation     `json:"relation"`
	Status        Status           `json:"status"`
	Channel       *channel.Summary `json:"channel,omitempty"`
	Contact       *contact.Summary `json:"contact,omitempty"`
	TeamID        *string          `json:"team_id,omitempty"`
	Assignee      *Actor           `json:"assignee,omitempty"`
	Preview       string           `json:"preview"`
	LastMessageAt time.Time        `json:"last_message_at"`
	LinkedAt      time.Time        `json:"linked_at"`
}

// TicketDTO 是接口中的工单视图，聚合了会话内核字段与工单扩展字段。
type TicketDTO struct {
	ID                  string                  `json:"id"`
	Number              int64                   `json:"number" doc:"工作区内递增编号，例如 1024"`
	TypeID              string                  `json:"type_id"`
	TypeName            string                  `json:"type_name"`
	Title               string                  `json:"title"`
	Description         string                  `json:"description"`
	Status              TicketStatus            `json:"status"`
	Visibility          tickettype.Visibility   `json:"visibility" doc:"all（客户可见）或 members（仅成员可见）"`
	TeamID              *string                 `json:"team_id,omitempty"`
	Assignee            *Actor                  `json:"assignee,omitempty"`
	Priority            Priority                `json:"priority"`
	CreatedBy           *string                 `json:"created_by,omitempty"`
	ResolvedAt          *time.Time              `json:"resolved_at,omitempty"`
	LastMessageAt       time.Time               `json:"last_message_at"`
	LastSeq             int64                   `json:"last_seq"`
	Unread              bool                    `json:"unread"`
	ReadSeq             int64                   `json:"read_seq"`
	Tags                []tag.Summary           `json:"tags"`
	Attributes          map[string]any          `json:"attributes"`
	LinkedCount         int                     `json:"linked_count" doc:"工单关联的服务型会话总数"`
	LinkedConversations []LinkedConversationDTO `json:"linked_conversations,omitempty" doc:"当前成员有权限看到的关联服务型会话列表（详情接口返回）"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
}

// TicketOutput 是返回单个工单的响应。
type TicketOutput struct {
	Body TicketDTO
}

// findVisibleTicket 读取 sub 在其工作区中可见的工单 id（同时加载 conversations 与 tickets）；
// 不存在、不是工单或不可见时返回 ErrNotFound。lock 为 true 时按 conversations -> tickets 顺序加行锁。
func findVisibleTicket(ctx context.Context, db bun.IDB, sub identity.Subject, id string, lock bool) (*Conversation, *TicketMeta, error) {
	c, err := findVisible(ctx, db, sub, id, lock)
	if err != nil {
		return nil, nil, err
	}
	if c.Kind != Ticket {
		return nil, nil, ErrNotFound.New()
	}
	tk := new(TicketMeta)
	q := db.NewSelect().Model(tk).Where("conversation_id = ? AND tenant_id = ?", c.ID, c.TenantID)
	if lock {
		q = q.For("UPDATE")
	}
	err = q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound.New()
	}
	return c, tk, errors.Wrap(err, "load ticket meta")
}

// toTicketDTOs 批量把工单会话转为 TicketDTO；includeLinks 为 true 时附带当前成员可见的关联服务型会话列表。
func toTicketDTOs(ctx context.Context, db bun.IDB, sub identity.Subject, convs []Conversation, includeLinks bool) ([]TicketDTO, error) {
	out := make([]TicketDTO, len(convs))
	if len(convs) == 0 {
		return out, nil
	}
	ids := make([]string, len(convs))
	for i := range convs {
		ids[i] = convs[i].ID
	}

	var metas []TicketMeta
	if err := db.NewSelect().Model(&metas).Where("tenant_id = ? AND conversation_id IN (?)", sub.Tenant, bun.In(ids)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load ticket metas")
	}
	metaByID := make(map[string]TicketMeta, len(metas))
	typeIDs := make([]string, 0, len(metas))
	for _, m := range metas {
		metaByID[m.ConversationID] = m
		typeIDs = append(typeIDs, m.TypeID)
	}

	typeNames := map[string]string{}
	if len(typeIDs) > 0 {
		var rows []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("ticket_types").Column("id", "name").
			Where("tenant_id = ? AND id IN (?)", sub.Tenant, bun.In(typeIDs)).Scan(ctx, &rows); err != nil {
			return nil, errors.Wrap(err, "load ticket type names")
		}
		for _, r := range rows {
			typeNames[r.ID] = r.Name
		}
	}

	var countRows []struct {
		TicketID string `bun:"ticket_id"`
		Count    int    `bun:"cnt"`
	}
	if err := db.NewSelect().Table("conversation_links").
		ColumnExpr("ticket_id, count(*) AS cnt").
		Where("tenant_id = ? AND ticket_id IN (?)", sub.Tenant, bun.In(ids)).
		GroupExpr("ticket_id").Scan(ctx, &countRows); err != nil {
		return nil, errors.Wrap(err, "count linked conversations")
	}
	linkedCounts := make(map[string]int, len(countRows))
	for _, r := range countRows {
		linkedCounts[r.TicketID] = r.Count
	}

	convTags, err := tag.ForConversations(ctx, db, ids)
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

	var linksByTicket map[string][]LinkedConversationDTO
	if includeLinks {
		linksByTicket, err = loadVisibleLinkedConversations(ctx, db, sub, ids)
		if err != nil {
			return nil, err
		}
	}

	for i, c := range convs {
		m := metaByID[c.ID]
		tags := convTags[c.ID]
		if tags == nil {
			tags = []tag.Summary{}
		}
		attrs := c.Attributes
		if attrs == nil {
			attrs = map[string]any{}
		}
		dto := TicketDTO{
			ID:            c.ID,
			Number:        m.Number,
			TypeID:        m.TypeID,
			TypeName:      typeNames[m.TypeID],
			Title:         m.Title,
			Description:   m.Description,
			Status:        TicketStatus(c.Status),
			Visibility:    m.Visibility,
			TeamID:        c.TeamID,
			Priority:      c.Priority,
			CreatedBy:     m.CreatedBy,
			ResolvedAt:    m.ResolvedAt,
			LastMessageAt: c.LastMessageAt,
			LastSeq:       c.LastSeq,
			Unread:        unread[c.ID],
			ReadSeq:       readSeq[c.ID],
			Tags:          tags,
			Attributes:    attrs,
			LinkedCount:   linkedCounts[c.ID],
			CreatedAt:     c.CreatedAt,
			UpdatedAt:     c.UpdatedAt,
		}
		if c.AssigneeType != "" {
			dto.Assignee = &Actor{Type: c.AssigneeType, ID: c.AssigneeID}
		}
		if includeLinks {
			list := linksByTicket[c.ID]
			if list == nil {
				list = []LinkedConversationDTO{}
			}
			dto.LinkedConversations = list
		}
		out[i] = dto
	}
	return out, nil
}

// loadVisibleLinkedConversations 批量加载工单 ticketIDs 下当前成员 sub 有权限查看的关联服务型会话摘要。
// 严格套用 visible() 权限过滤，防止不同团队或代理之间泄露权限之外的客户会话。
func loadVisibleLinkedConversations(ctx context.Context, db bun.IDB, sub identity.Subject, ticketIDs []string) (map[string][]LinkedConversationDTO, error) {
	result := make(map[string][]LinkedConversationDTO, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return result, nil
	}
	var links []ConversationLink
	if err := db.NewSelect().Model(&links).
		Where("tenant_id = ? AND ticket_id IN (?)", sub.Tenant, bun.In(ticketIDs)).
		OrderExpr("created_at ASC, id ASC").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load conversation links")
	}
	if len(links) == 0 {
		return result, nil
	}
	convIDSet := make(map[string]struct{}, len(links))
	convIDs := make([]string, 0, len(links))
	for _, l := range links {
		if _, dup := convIDSet[l.ConversationID]; !dup {
			convIDSet[l.ConversationID] = struct{}{}
			convIDs = append(convIDs, l.ConversationID)
		}
	}

	var convs []Conversation
	q := db.NewSelect().Model(&convs).
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.id IN (?)", sub.Tenant, Service, bun.In(convIDs))
	q = visible(q, sub)
	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load visible linked conversations")
	}
	if len(convs) == 0 {
		return result, nil
	}
	dtos, err := toDTOs(ctx, db, sub, convs)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]ConversationDTO, len(dtos))
	for _, d := range dtos {
		byID[d.ID] = d
	}
	for _, l := range links {
		d, ok := byID[l.ConversationID]
		if !ok {
			continue
		}
		result[l.TicketID] = append(result[l.TicketID], LinkedConversationDTO{
			ID:            d.ID,
			Relation:      l.Relation,
			Status:        d.Status,
			Channel:       d.Channel,
			Contact:       d.Contact,
			TeamID:        d.TeamID,
			Assignee:      d.Assignee,
			Preview:       d.Preview,
			LastMessageAt: d.LastMessageAt,
			LinkedAt:      l.CreatedAt,
		})
	}
	return result, nil
}

// resumeWaitingTickets 在客户往服务型会话 serviceConvID 发来新消息的同一事务中，
// 将关联到此会话且正处于 waiting_on_customer 状态的工单自动切回 in_progress 并记系统事件。
func resumeWaitingTickets(ctx context.Context, tx database.Tx, down Downstream, tenantID, serviceConvID string, at time.Time) error {
	var ticketIDs []string
	err := tx.NewSelect().TableExpr("conversation_links cl").
		Join("JOIN conversations tc ON tc.id = cl.ticket_id").
		ColumnExpr("tc.id").
		Where("cl.tenant_id = ? AND cl.conversation_id = ? AND tc.tenant_id = ? AND tc.kind = ? AND tc.status = ?",
			tenantID, serviceConvID, tenantID, Ticket, TicketWaiting).
		OrderExpr("tc.id ASC").
		Scan(ctx, &ticketIDs)
	if err != nil {
		return errors.Wrap(err, "find waiting tickets for customer reply")
	}
	if len(ticketIDs) == 0 {
		return nil
	}
	sys := identity.Subject{Type: identity.System, ID: "ticket", Tenant: tenantID}
	for _, tid := range ticketIDs {
		tc, err := find(ctx, tx, tenantID, tid, true)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return err
		}
		if tc.Kind != Ticket || TicketStatus(tc.Status) != TicketWaiting {
			continue
		}
		tc.Status = Status(TicketInProgress)
		tc.LastMessageAt = at
		ev := event(sys, "ticket.status_changed", map[string]any{
			"from":            string(TicketWaiting),
			"to":              string(TicketInProgress),
			"reason":          "customer_replied",
			"conversation_id": serviceConvID,
		})
		if err := commit(ctx, tx, down, tc, ev); err != nil {
			return err
		}
	}
	return nil
}

// nextTicketNumber 在事务 tx 中原子递增工作区 tenantID 的工单编号并返回本次分配的编号。
func nextTicketNumber(ctx context.Context, tx bun.IDB, tenantID string) (int64, error) {
	var num int64
	err := tx.NewRaw(`UPDATE tenants SET next_ticket_number = next_ticket_number + 1 WHERE id = ? RETURNING next_ticket_number - 1`, tenantID).Scan(ctx, &num)
	return num, errors.Wrap(err, "allocate ticket number")
}
