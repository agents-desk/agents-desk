package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/tag"
	"agents-desk/internal/tickettype"
)

// TicketMeta 是 tickets 扩展表的数据库模型，与 conversations（kind=ticket）一对一关联。
type TicketMeta struct {
	bun.BaseModel `bun:"table:tickets"`

	ConversationID string `bun:"conversation_id,pk"`
	database.Timestamps
	TenantID    string                `bun:"tenant_id,notnull"`
	Number      int64                 `bun:"number,notnull"`
	TypeID      *string               `bun:"type_id"`
	Title       string                `bun:"title,notnull"`
	Description string                `bun:"description,notnull"`
	Visibility  tickettype.Visibility `bun:"visibility,notnull"`
	CreatedBy   *string               `bun:"created_by"`
	ResolvedAt  *time.Time            `bun:"resolved_at"`
}

// TicketContact 是 ticket_contacts 关联表的数据库模型，多对多连接工单与受影响联系人。
type TicketContact struct {
	bun.BaseModel `bun:"table:ticket_contacts"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID             string  `bun:"tenant_id,notnull"`
	TicketID             string  `bun:"ticket_id,notnull"`
	ContactID            string  `bun:"contact_id,notnull"`
	SourceConversationID *string `bun:"source_conversation_id"`
	CreatedBy            *string `bun:"created_by"`
}

// LinkedContactDTO 是工单详情中展示的一位已关联受影响客户摘要（仅返回当前成员有权限看到的客户）。
type LinkedContactDTO struct {
	Contact            contact.Summary  `json:"contact"`
	ConversationID     *string          `json:"conversation_id,omitempty" doc:"当前成员可见的该客户活跃或最近一条服务型会话 ID"`
	ConversationStatus Status           `json:"conversation_status,omitempty"`
	Channel            *channel.Summary `json:"channel,omitempty"`
	Preview            string           `json:"preview"`
	LastMessageAt      *time.Time       `json:"last_message_at,omitempty"`
	IsSource           bool             `json:"is_source" doc:"是否由该客户的服务型会话转成此工单"`
	LinkedAt           time.Time        `json:"linked_at"`
}

// TicketDTO 是接口中的工单视图，聚合了会话内核字段与工单扩展字段。
type TicketDTO struct {
	ID             string                `json:"id"`
	Number         int64                 `json:"number" doc:"工作区内递增编号，例如 1024"`
	TypeID         *string               `json:"type_id,omitempty" doc:"工单类型 ID；为空表示通用工单"`
	TypeName       string                `json:"type_name,omitempty"`
	Title          string                `json:"title"`
	Description    string                `json:"description"`
	Status         TicketStatus          `json:"status"`
	Visibility     tickettype.Visibility `json:"visibility" doc:"all（客户可见）或 members（仅成员可见）"`
	TeamID         *string               `json:"team_id,omitempty"`
	Assignee       *Actor                `json:"assignee,omitempty"`
	Priority       Priority              `json:"priority"`
	CreatedBy      *string               `json:"created_by,omitempty"`
	ResolvedAt     *time.Time            `json:"resolved_at,omitempty"`
	LastMessageAt  time.Time             `json:"last_message_at"`
	LastSeq        int64                 `json:"last_seq"`
	Unread         bool                  `json:"unread"`
	ReadSeq        int64                 `json:"read_seq"`
	Tags           []tag.Summary         `json:"tags"`
	Attributes     map[string]any        `json:"attributes"`
	LinkedCount    int                   `json:"linked_count" doc:"工单关联的受影响客户总数"`
	LinkedContacts []LinkedContactDTO    `json:"linked_contacts,omitempty" doc:"当前成员有权限看到的受影响客户列表（详情接口返回）"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
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

// toTicketDTOs 批量把工单会话转为 TicketDTO；includeLinks 为 true 时附带当前成员可见的关联受影响客户列表。
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
	typeIDSet := map[string]struct{}{}
	typeIDs := make([]string, 0, len(metas))
	for _, m := range metas {
		metaByID[m.ConversationID] = m
		if m.TypeID != nil && *m.TypeID != "" {
			if _, ok := typeIDSet[*m.TypeID]; !ok {
				typeIDSet[*m.TypeID] = struct{}{}
				typeIDs = append(typeIDs, *m.TypeID)
			}
		}
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
	if err := db.NewSelect().Table("ticket_contacts").
		ColumnExpr("ticket_id, count(*) AS cnt").
		Where("tenant_id = ? AND ticket_id IN (?)", sub.Tenant, bun.In(ids)).
		GroupExpr("ticket_id").Scan(ctx, &countRows); err != nil {
		return nil, errors.Wrap(err, "count ticket contacts")
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

	var linksByTicket map[string][]LinkedContactDTO
	if includeLinks {
		linksByTicket, err = loadVisibleLinkedContacts(ctx, db, sub, ids)
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
		var typeName string
		if m.TypeID != nil {
			typeName = typeNames[*m.TypeID]
		}
		dto := TicketDTO{
			ID:            c.ID,
			Number:        m.Number,
			TypeID:        m.TypeID,
			TypeName:      typeName,
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
				list = []LinkedContactDTO{}
			}
			dto.LinkedContacts = list
		}
		out[i] = dto
	}
	return out, nil
}

// loadVisibleLinkedContacts 批量加载工单 ticketIDs 下当前成员 sub 有权限查看的受影响客户摘要。
// 对受限成员（ScopeTeam/ScopeAssigned）严格校验是否可见该客户的服务型会话，防止不同团队或代理之间泄露权限之外的客户。
func loadVisibleLinkedContacts(ctx context.Context, db bun.IDB, sub identity.Subject, ticketIDs []string) (map[string][]LinkedContactDTO, error) {
	result := make(map[string][]LinkedContactDTO, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return result, nil
	}
	var links []TicketContact
	if err := db.NewSelect().Model(&links).
		Where("tenant_id = ? AND ticket_id IN (?)", sub.Tenant, bun.In(ticketIDs)).
		OrderExpr("(source_conversation_id IS NOT NULL) DESC, created_at ASC, id ASC").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load ticket contacts")
	}
	if len(links) == 0 {
		return result, nil
	}

	contactIDSet := make(map[string]struct{}, len(links))
	contactIDs := make([]string, 0, len(links))
	for _, l := range links {
		if _, dup := contactIDSet[l.ContactID]; !dup {
			contactIDSet[l.ContactID] = struct{}{}
			contactIDs = append(contactIDs, l.ContactID)
		}
	}

	// 加载这些联系人在当前成员权限内可见的全部服务型会话，按活跃优先、最近消息倒序排列
	var convs []Conversation
	q := db.NewSelect().Model(&convs).
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.contact_id IN (?)", sub.Tenant, Service, bun.In(contactIDs)).
		OrderExpr("(conversation.status IN ('open', 'snoozed')) DESC, conversation.last_message_at DESC, conversation.id DESC")
	q = visible(q, sub)
	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load visible service conversations for ticket contacts")
	}

	// 按每个联系人挑出最佳可见会话（优先活跃会话，其次按最近消息）及按 ID 索引
	convsByContact := map[string][]Conversation{}
	convByID := map[string]Conversation{}
	for _, c := range convs {
		if c.ContactID == nil {
			continue
		}
		convsByContact[*c.ContactID] = append(convsByContact[*c.ContactID], c)
		convByID[c.ID] = c
	}

	// 受限成员只保留名下有可见服务型会话的联系人；ScopeAll 和系统主体可见全部关联联系人
	seeAll := sub.Type == identity.System || auth.ScopeOf(sub) == auth.ScopeAll
	visibleContactIDs := make([]string, 0, len(contactIDs))
	chosenConvs := make([]Conversation, 0, len(contactIDs))
	chosenByContact := map[string]string{}
	for _, cid := range contactIDs {
		list := convsByContact[cid]
		if !seeAll && len(list) == 0 {
			continue
		}
		visibleContactIDs = append(visibleContactIDs, cid)
		if len(list) > 0 {
			chosenByContact[cid] = list[0].ID
			chosenConvs = append(chosenConvs, list[0])
		}
	}
	if len(visibleContactIDs) == 0 {
		return result, nil
	}

	var contactRows []contact.Contact
	if err := db.NewSelect().Model(&contactRows).Where("tenant_id = ? AND id IN (?)", sub.Tenant, bun.In(visibleContactIDs)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load contacts for ticket links")
	}
	contactByID := make(map[string]contact.Summary, len(contactRows))
	for i := range contactRows {
		contactByID[contactRows[i].ID] = contactRows[i].Summary()
	}

	convDTOByID := map[string]ConversationDTO{}
	if len(chosenConvs) > 0 {
		dtos, err := toDTOs(ctx, db, sub, chosenConvs)
		if err != nil {
			return nil, err
		}
		for _, d := range dtos {
			convDTOByID[d.ID] = d
		}
	}

	for _, l := range links {
		ct, ok := contactByID[l.ContactID]
		if !ok {
			continue
		}
		item := LinkedContactDTO{
			Contact:  ct,
			IsSource: l.SourceConversationID != nil && *l.SourceConversationID != "",
			LinkedAt: l.CreatedAt,
		}
		if convID := chosenByContact[l.ContactID]; convID != "" {
			if d, ok := convDTOByID[convID]; ok {
				id := d.ID
				ts := d.LastMessageAt
				item.ConversationID = &id
				item.ConversationStatus = d.Status
				item.Channel = d.Channel
				item.Preview = d.Preview
				item.LastMessageAt = &ts
			}
		} else if l.SourceConversationID != nil {
			if _, ok := convByID[*l.SourceConversationID]; ok {
				id := *l.SourceConversationID
				item.ConversationID = &id
			}
		}
		result[l.TicketID] = append(result[l.TicketID], item)
	}
	return result, nil
}

// resumeWaitingTickets 在客户往服务型会话 c 发来新消息的同一事务中，
// 将关联到此客户、且正处于 waiting_on_customer 状态的工单自动切回 in_progress 并记系统事件。
func resumeWaitingTickets(ctx context.Context, tx database.Tx, down Downstream, tenantID string, c *Conversation, _ string, at time.Time) error {
	if c.ContactID == nil || *c.ContactID == "" {
		return nil
	}
	var ticketIDs []string
	err := tx.NewSelect().TableExpr("ticket_contacts tc").
		Join("JOIN conversations conv ON conv.id = tc.ticket_id").
		ColumnExpr("DISTINCT conv.id").
		Where("tc.tenant_id = ? AND tc.contact_id = ? AND conv.tenant_id = ? AND conv.kind = ? AND conv.status = ?",
			tenantID, *c.ContactID, tenantID, Ticket, TicketWaiting).
		OrderExpr("conv.id ASC").Scan(ctx, &ticketIDs)
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
			"conversation_id": c.ID,
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
