package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/contact"
	"agents-desk/internal/tickettype"
)

// ContactTicketDTO 是客户在网页聊天插件中看到的工单摘要，仅暴露对外可见（visibility=all）的工单。
type ContactTicketDTO struct {
	ID             string       `json:"id"`
	Number         int64        `json:"number" doc:"工单编号"`
	TypeName       string       `json:"type_name" doc:"工单类型名称"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	Status         TicketStatus `json:"status" doc:"submitted、in_progress、waiting_on_customer 或 resolved"`
	ConversationID string       `json:"conversation_id" doc:"优先匹配当前渠道的关联服务型会话 ID，供客户跳转补充信息"`
	LatestUpdate   string       `json:"latest_update" doc:"最近一条公开进度的文字摘要"`
	LastMessageAt  time.Time    `json:"last_message_at"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// ContactTicketDetailDTO 是客户在网页聊天插件中看到的工单详情，包含全部对外公开进度记录。
type ContactTicketDetailDTO struct {
	ContactTicketDTO
	Updates []ContactItemDTO `json:"updates" nullable:"false" doc:"工单的公开进度记录，按时间顺序排列"`
}

// ListTicketsForContact 返回客户在网页聊天插件中可见的工单列表。
type ListTicketsForContact struct {
	db *bun.DB
}

// NewListTicketsForContact 返回查询客户可见工单列表的动作。
func NewListTicketsForContact(db *bun.DB) *ListTicketsForContact {
	return &ListTicketsForContact{db: db}
}

// Handle 返回联系人 sub 在工作区中关联到的客户可见（visibility=all）工单，按最近活动倒序排列，最多 50 条。
// 已登录用户（contact.User）以 contact_id 为主体跨渠道可见全部关联工单，不限定当前插件渠道；
// 访客线索（contact.Lead）仅返回与当前渠道 channelID 会话关联的工单。
func (a *ListTicketsForContact) Handle(ctx context.Context, sub identity.Subject, channelID string) ([]ContactTicketDTO, error) {
	if sub.Type != contact.Type {
		return nil, ErrForbidden.New()
	}
	kind, exists, err := contactKindOf(ctx, a.db, sub)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []ContactTicketDTO{}, nil
	}

	var convs []Conversation
	q := a.db.NewSelect().Model(&convs).
		Join("JOIN tickets tk ON tk.conversation_id = conversation.id").
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND tk.visibility = ?", sub.Tenant, Ticket, tickettype.VisibilityAll)
	if kind == contact.User {
		q = q.Where(`EXISTS (
			SELECT 1 FROM conversation_links cl
			JOIN conversations sc ON sc.id = cl.conversation_id
			WHERE cl.tenant_id = ? AND cl.ticket_id = conversation.id
				AND sc.tenant_id = ? AND sc.kind = ? AND sc.contact_id = ?
		)`, sub.Tenant, sub.Tenant, Service, sub.ID)
	} else {
		q = q.Where(`EXISTS (
			SELECT 1 FROM conversation_links cl
			JOIN conversations sc ON sc.id = cl.conversation_id
			WHERE cl.tenant_id = ? AND cl.ticket_id = conversation.id
				AND sc.tenant_id = ? AND sc.kind = ? AND sc.contact_id = ? AND sc.channel_id = ?
		)`, sub.Tenant, sub.Tenant, Service, sub.ID, channelID)
	}
	if err := q.OrderExpr("conversation.last_message_at DESC, conversation.id DESC").Limit(contactListLimit).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list contact tickets")
	}
	return toContactTicketDTOs(ctx, a.db, sub, kind, channelID, convs)
}

// GetTicketForContact 返回客户在网页聊天插件中查看的单张工单详情与公开进度列表。
type GetTicketForContact struct {
	db    *bun.DB
	media Media
}

// NewGetTicketForContact 返回读取客户可见工单详情的动作。
func NewGetTicketForContact(db *bun.DB, media Media) *GetTicketForContact {
	return &GetTicketForContact{db: db, media: media}
}

// Handle 返回联系人 sub 可见的工单 id 详情及其公开进度（visibility=all）；
// 非客户可见工单、未关联该联系人或不存在均返回 ErrNotFound。
func (a *GetTicketForContact) Handle(ctx context.Context, sub identity.Subject, channelID, id string) (*ContactTicketDetailDTO, error) {
	if sub.Type != contact.Type {
		return nil, ErrForbidden.New()
	}
	kind, exists, err := contactKindOf(ctx, a.db, sub)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound.New()
	}

	var conv Conversation
	q := a.db.NewSelect().Model(&conv).
		Join("JOIN tickets tk ON tk.conversation_id = conversation.id").
		Where("conversation.id = ? AND conversation.tenant_id = ? AND conversation.kind = ? AND tk.visibility = ?", id, sub.Tenant, Ticket, tickettype.VisibilityAll)
	if kind == contact.User {
		q = q.Where(`EXISTS (
			SELECT 1 FROM conversation_links cl
			JOIN conversations sc ON sc.id = cl.conversation_id
			WHERE cl.tenant_id = ? AND cl.ticket_id = conversation.id
				AND sc.tenant_id = ? AND sc.kind = ? AND sc.contact_id = ?
		)`, sub.Tenant, sub.Tenant, Service, sub.ID)
	} else {
		q = q.Where(`EXISTS (
			SELECT 1 FROM conversation_links cl
			JOIN conversations sc ON sc.id = cl.conversation_id
			WHERE cl.tenant_id = ? AND cl.ticket_id = conversation.id
				AND sc.tenant_id = ? AND sc.kind = ? AND sc.contact_id = ? AND sc.channel_id = ?
		)`, sub.Tenant, sub.Tenant, Service, sub.ID, channelID)
	}
	err = q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "load contact ticket")
	}

	summaries, err := toContactTicketDTOs(ctx, a.db, sub, kind, channelID, []Conversation{conv})
	if err != nil {
		return nil, err
	}

	var items []Item
	if err := a.db.NewSelect().Model(&items).
		Where("tenant_id = ? AND conversation_id = ? AND kind = ? AND visibility = ?", sub.Tenant, conv.ID, Message, Everyone).
		OrderExpr("seq ASC").
		Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load contact ticket updates")
	}
	updates, err := toContactItemDTOs(ctx, a.db, a.media, items)
	if err != nil {
		return nil, err
	}
	return &ContactTicketDetailDTO{
		ContactTicketDTO: summaries[0],
		Updates:          updates,
	}, nil
}

// contactKindOf 查询联系人 sub 的类型（user 或 lead）；联系人不存在时返回 exists=false。
func contactKindOf(ctx context.Context, db bun.IDB, sub identity.Subject) (contact.Kind, bool, error) {
	var kind contact.Kind
	err := db.NewSelect().Table("contacts").Column("kind").Where("id = ? AND tenant_id = ?", sub.ID, sub.Tenant).Scan(ctx, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.Wrap(err, "load contact kind")
	}
	return kind, true, nil
}

// toContactTicketDTOs 批量组装客户侧工单摘要：读取工单元数据、类型名、最新公开进度，
// 并为每张工单挑选最佳关联服务型会话 ID（优先当前插件渠道，其次该联系人最近活跃的关联会话）。
func toContactTicketDTOs(ctx context.Context, db bun.IDB, sub identity.Subject, kind contact.Kind, channelID string, convs []Conversation) ([]ContactTicketDTO, error) {
	out := make([]ContactTicketDTO, len(convs))
	if len(convs) == 0 {
		return out, nil
	}
	ids := make([]string, len(convs))
	for i := range convs {
		ids[i] = convs[i].ID
	}

	var metas []TicketMeta
	if err := db.NewSelect().Model(&metas).Where("tenant_id = ? AND conversation_id IN (?)", sub.Tenant, bun.In(ids)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load contact ticket metas")
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
			return nil, errors.Wrap(err, "load contact ticket type names")
		}
		for _, r := range rows {
			typeNames[r.ID] = r.Name
		}
	}

	// 读取每张工单最近一条公开进度消息文字
	var latestRows []struct {
		TicketID string `bun:"ticket_id"`
		Text     string `bun:"text"`
	}
	if err := db.NewRaw(`SELECT DISTINCT ON (conversation_id) conversation_id AS ticket_id, COALESCE(content->>'text', '') AS text
		FROM timeline_items
		WHERE tenant_id = ? AND conversation_id IN (?) AND kind = ? AND visibility = ?
		ORDER BY conversation_id, seq DESC`,
		sub.Tenant, bun.In(ids), Message, Everyone).Scan(ctx, &latestRows); err != nil {
		return nil, errors.Wrap(err, "load contact ticket latest update")
	}
	latestByID := make(map[string]string, len(latestRows))
	for _, r := range latestRows {
		latestByID[r.TicketID] = r.Text
	}

	// 为每张工单选出该联系人可打开的关联服务型会话 ID：优先当前插件渠道，其次最近活跃的关联会话
	var linkRows []struct {
		TicketID       string `bun:"ticket_id"`
		ConversationID string `bun:"conversation_id"`
	}
	linkQ := db.NewSelect().TableExpr("conversation_links AS cl").
		Join("JOIN conversations AS sc ON sc.id = cl.conversation_id").
		ColumnExpr("DISTINCT ON (cl.ticket_id) cl.ticket_id, cl.conversation_id").
		Where("cl.tenant_id = ? AND cl.ticket_id IN (?) AND sc.tenant_id = ? AND sc.kind = ? AND sc.contact_id = ?",
			sub.Tenant, bun.In(ids), sub.Tenant, Service, sub.ID)
	if kind != contact.User {
		linkQ = linkQ.Where("sc.channel_id = ?", channelID)
	}
	linkQ = linkQ.OrderExpr("cl.ticket_id, (sc.channel_id = ?) DESC, sc.last_message_at DESC, sc.id DESC", channelID)
	if err := linkQ.Scan(ctx, &linkRows); err != nil {
		return nil, errors.Wrap(err, "load contact ticket linked conversation")
	}
	convByTicket := make(map[string]string, len(linkRows))
	for _, r := range linkRows {
		convByTicket[r.TicketID] = r.ConversationID
	}

	for i, c := range convs {
		m := metaByID[c.ID]
		out[i] = ContactTicketDTO{
			ID:             c.ID,
			Number:         m.Number,
			TypeName:       typeNames[m.TypeID],
			Title:          m.Title,
			Description:    m.Description,
			Status:         TicketStatus(c.Status),
			ConversationID: convByTicket[c.ID],
			LatestUpdate:   latestByID[c.ID],
			LastMessageAt:  c.LastMessageAt,
			CreatedAt:      c.CreatedAt,
			UpdatedAt:      c.UpdatedAt,
		}
	}
	return out, nil
}
