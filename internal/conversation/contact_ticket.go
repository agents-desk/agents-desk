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

// contactTicketLimit 是客户侧工单列表最多返回的工单数。
const contactTicketLimit = 50

// ContactTicketDTO 是客户在网页聊天插件中看到的工单摘要，仅暴露对外可见（visibility=all）的工单。
type ContactTicketDTO struct {
	ID            string       `json:"id"`
	Number        int64        `json:"number" doc:"工单编号"`
	TypeName      string       `json:"type_name" doc:"工单类型名称"`
	Title         string       `json:"title"`
	Description   string       `json:"description"`
	Status        TicketStatus `json:"status" doc:"submitted、in_progress、waiting_on_customer 或 resolved"`
	LatestUpdate  string       `json:"latest_update" doc:"最近一条公开进度的文字摘要"`
	LastMessageAt time.Time    `json:"last_message_at"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
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
	kind, contactLang, exists, err := contactKindAndLangOf(ctx, a.db, sub)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []ContactTicketDTO{}, nil
	}

	var convs []Conversation
	q := a.db.NewSelect().Model(&convs).
		Join("JOIN tickets tk ON tk.conversation_id = conversation.id").
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND tk.visibility = ?", sub.Tenant, Ticket, tickettype.VisibilityAll).
		Where("EXISTS (SELECT 1 FROM ticket_contacts tc WHERE tc.tenant_id = ? AND tc.ticket_id = conversation.id AND tc.contact_id = ?)", sub.Tenant, sub.ID)
	if kind != contact.User {
		q = q.Where("EXISTS (SELECT 1 FROM conversations sc WHERE sc.tenant_id = ? AND sc.kind = ? AND sc.contact_id = ? AND sc.channel_id = ?)",
			sub.Tenant, Service, sub.ID, channelID)
	}
	if err := q.OrderExpr("conversation.last_message_at DESC, conversation.id DESC").Limit(contactTicketLimit).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list contact tickets")
	}
	return toContactTicketDTOs(ctx, a.db, sub, convs, contactLang)
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
	kind, contactLang, exists, err := contactKindAndLangOf(ctx, a.db, sub)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound.New()
	}

	var conv Conversation
	q := a.db.NewSelect().Model(&conv).
		Join("JOIN tickets tk ON tk.conversation_id = conversation.id").
		Where("conversation.id = ? AND conversation.tenant_id = ? AND conversation.kind = ? AND tk.visibility = ?", id, sub.Tenant, Ticket, tickettype.VisibilityAll).
		Where("EXISTS (SELECT 1 FROM ticket_contacts tc WHERE tc.tenant_id = ? AND tc.ticket_id = conversation.id AND tc.contact_id = ?)", sub.Tenant, sub.ID)
	if kind != contact.User {
		q = q.Where("EXISTS (SELECT 1 FROM conversations sc WHERE sc.tenant_id = ? AND sc.kind = ? AND sc.contact_id = ? AND sc.channel_id = ?)",
			sub.Tenant, Service, sub.ID, channelID)
	}
	err = q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "load contact ticket")
	}

	summaries, err := toContactTicketDTOs(ctx, a.db, sub, []Conversation{conv}, contactLang)
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
	if contactLang != "" {
		for i := range updates {
			if tr := translationInMap(derivedMap(items[i].Derived, "translations"), contactLang); tr != "" {
				updates[i].Text = tr
			}
		}
	}
	return &ContactTicketDetailDTO{
		ContactTicketDTO: summaries[0],
		Updates:          updates,
	}, nil
}

// contactKindOf 查询联系人 sub 的类型（user 或 lead）；联系人不存在时返回 exists=false。
func contactKindOf(ctx context.Context, db bun.IDB, sub identity.Subject) (contact.Kind, bool, error) {
	kind, _, ok, err := contactKindAndLangOf(ctx, db, sub)
	return kind, ok, err
}

// contactKindAndLangOf 查询联系人 sub 的类型（user 或 lead）与语言；联系人不存在时返回 exists=false。
func contactKindAndLangOf(ctx context.Context, db bun.IDB, sub identity.Subject) (contact.Kind, string, bool, error) {
	var row struct {
		Kind     contact.Kind `bun:"kind"`
		Language string       `bun:"language"`
	}
	err := db.NewSelect().Table("contacts").Column("kind", "language").Where("id = ? AND tenant_id = ?", sub.ID, sub.Tenant).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, errors.Wrap(err, "load contact kind and language")
	}
	return row.Kind, row.Language, true, nil
}

// toContactTicketDTOs 批量组装客户侧工单摘要：读取工单元数据、类型名与最新公开进度，并优先按 contactLang 展示对应译文。
func toContactTicketDTOs(ctx context.Context, db bun.IDB, sub identity.Subject, convs []Conversation, contactLang string) ([]ContactTicketDTO, error) {
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
			return nil, errors.Wrap(err, "load contact ticket type names")
		}
		for _, r := range rows {
			typeNames[r.ID] = r.Name
		}
	}

	// 读取每张工单最近一条公开进度消息文字及其派生译文
	var latestRows []struct {
		TicketID string         `bun:"ticket_id"`
		Text     string         `bun:"text"`
		Derived  map[string]any `bun:"derived,type:jsonb"`
	}
	if err := db.NewRaw(`SELECT DISTINCT ON (conversation_id) conversation_id AS ticket_id, COALESCE(content->>'text', '') AS text, derived
		FROM timeline_items
		WHERE tenant_id = ? AND conversation_id IN (?) AND kind = ? AND visibility = ?
		ORDER BY conversation_id, seq DESC`,
		sub.Tenant, bun.In(ids), Message, Everyone).Scan(ctx, &latestRows); err != nil {
		return nil, errors.Wrap(err, "load contact ticket latest update")
	}
	latestByID := make(map[string]string, len(latestRows))
	for _, r := range latestRows {
		text := r.Text
		if tr := translationInMap(derivedMap(r.Derived, "translations"), contactLang); tr != "" {
			text = tr
		}
		latestByID[r.TicketID] = text
	}

	for i, c := range convs {
		m := metaByID[c.ID]
		var typeName string
		if m.TypeID != nil {
			typeName = typeNames[*m.TypeID]
		}
		title := m.Title
		if tr := translationInMap(derivedMap(m.Derived, "title"), contactLang); tr != "" {
			title = tr
		}
		desc := m.Description
		if tr := translationInMap(derivedMap(m.Derived, "description"), contactLang); tr != "" {
			desc = tr
		}
		out[i] = ContactTicketDTO{
			ID:            c.ID,
			Number:        m.Number,
			TypeName:      typeName,
			Title:         title,
			Description:   desc,
			Status:        TicketStatus(c.Status),
			LatestUpdate:  latestByID[c.ID],
			LastMessageAt: c.LastMessageAt,
			CreatedAt:     c.CreatedAt,
			UpdatedAt:     c.UpdatedAt,
		}
	}
	return out, nil
}
