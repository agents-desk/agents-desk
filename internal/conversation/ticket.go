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
	"agents-desk/internal/extgroup"
	"agents-desk/internal/lang"
	"agents-desk/internal/push"
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
	Derived     map[string]any        `bun:"derived,type:jsonb,notnull"`
}

// TicketTarget 是 ticket_targets 关联表的数据库模型，多对多连接工单与受影响联系人或外部群。
type TicketTarget struct {
	bun.BaseModel `bun:"table:ticket_targets"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID             string  `bun:"tenant_id,notnull"`
	TicketID             string  `bun:"ticket_id,notnull"`
	ContactID            *string `bun:"contact_id"`
	ExternalGroupID      *string `bun:"external_group_id"`
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

// LinkedGroupDTO 是工单详情中展示的一个已关联外部群摘要（仅返回当前成员有权限看到的外部群）。
type LinkedGroupDTO struct {
	Group              extgroup.Summary `json:"group"`
	ConversationID     *string          `json:"conversation_id,omitempty" doc:"当前成员可见的该外部群活跃或最近一条服务型会话 ID"`
	ConversationStatus Status           `json:"conversation_status,omitempty"`
	Channel            *channel.Summary `json:"channel,omitempty"`
	Preview            string           `json:"preview"`
	LastMessageAt      *time.Time       `json:"last_message_at,omitempty"`
	IsSource           bool             `json:"is_source" doc:"是否由该外部群的服务型会话转成此工单"`
	LinkedAt           time.Time        `json:"linked_at"`
}

// TicketDTO 是接口中的工单视图，聚合了会话内核字段与工单扩展字段。
type TicketDTO struct {
	ID                    string                `json:"id"`
	Number                int64                 `json:"number" doc:"工作区内递增编号，例如 1024"`
	TypeID                *string               `json:"type_id,omitempty" doc:"工单类型 ID；为空表示通用工单"`
	TypeName              string                `json:"type_name,omitempty"`
	Title                 string                `json:"title"`
	Description           string                `json:"description"`
	Language              string                `json:"language,omitempty" doc:"工单标题与描述识别出的源语言（BCP 47）"`
	TitleTranslated       string                `json:"title_translated,omitempty" doc:"当前查看成员语言的工单标题译文；同语言或暂无时为空"`
	DescriptionTranslated string                `json:"description_translated,omitempty" doc:"当前查看成员语言的工单描述译文；同语言或暂无时为空"`
	MetaUntranslated      bool                  `json:"meta_untranslated,omitempty" doc:"工单标题或描述对当前查看成员是否还缺译文"`
	Status                TicketStatus          `json:"status"`
	Visibility            tickettype.Visibility `json:"visibility" doc:"all（客户可见）或 members（仅成员可见）"`
	TeamID                *string               `json:"team_id,omitempty"`
	Assignee              *Actor                `json:"assignee,omitempty"`
	Priority              Priority              `json:"priority"`
	CreatedBy             *string               `json:"created_by,omitempty"`
	ResolvedAt            *time.Time            `json:"resolved_at,omitempty"`
	LastMessageAt         time.Time             `json:"last_message_at"`
	LastSeq               int64                 `json:"last_seq"`
	Unread                bool                  `json:"unread"`
	ReadSeq               int64                 `json:"read_seq"`
	Tags                  []tag.Summary         `json:"tags"`
	Attributes            map[string]any        `json:"attributes"`
	LinkedCount           int                   `json:"linked_count" doc:"工单关联的受影响对象总数（联系人与外部群之和）"`
	LinkedContacts        []LinkedContactDTO    `json:"linked_contacts,omitempty" doc:"当前成员有权限看到的受影响联系人列表（详情接口返回）"`
	LinkedGroups          []LinkedGroupDTO      `json:"linked_groups,omitempty" doc:"当前成员有权限看到的受影响外部群列表（详情接口返回）"`
	CreatedAt             time.Time             `json:"created_at"`
	UpdatedAt             time.Time             `json:"updated_at"`
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
	if err := db.NewSelect().Table("ticket_targets").
		ColumnExpr("ticket_id, count(*) AS cnt").
		Where("tenant_id = ? AND ticket_id IN (?)", sub.Tenant, bun.In(ids)).
		GroupExpr("ticket_id").Scan(ctx, &countRows); err != nil {
		return nil, errors.Wrap(err, "count ticket targets")
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
	viewerLang, err := MemberLanguage(ctx, db, sub)
	if err != nil {
		return nil, err
	}

	var linksByTicket map[string][]LinkedContactDTO
	var groupsByTicket map[string][]LinkedGroupDTO
	if includeLinks {
		linksByTicket, groupsByTicket, err = loadVisibleLinkedTargets(ctx, db, sub, ids)
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
		metaLang, _ := m.Derived["language"].(string)
		titleTr := translationInMap(derivedMap(m.Derived, "title"), viewerLang)
		descTr := translationInMap(derivedMap(m.Derived, "description"), viewerLang)
		metaUntranslated := false
		if viewerLang != "" && (m.Title != "" || m.Description != "") {
			metaUntranslated = len(TicketMetaMissingLanguages(m.Title, m.Description, m.Derived, []string{viewerLang})) > 0
		}
		dto := TicketDTO{
			ID:                    c.ID,
			Number:                m.Number,
			TypeID:                m.TypeID,
			TypeName:              typeName,
			Title:                 m.Title,
			Description:           m.Description,
			Language:              metaLang,
			TitleTranslated:       titleTr,
			DescriptionTranslated: descTr,
			MetaUntranslated:      metaUntranslated,
			Status:                TicketStatus(c.Status),
			Visibility:            m.Visibility,
			TeamID:                c.TeamID,
			Priority:              c.Priority,
			CreatedBy:             m.CreatedBy,
			ResolvedAt:            m.ResolvedAt,
			LastMessageAt:         c.LastMessageAt,
			LastSeq:               c.LastSeq,
			Unread:                unread[c.ID],
			ReadSeq:               readSeq[c.ID],
			Tags:                  tags,
			Attributes:            attrs,
			LinkedCount:           linkedCounts[c.ID],
			CreatedAt:             c.CreatedAt,
			UpdatedAt:             c.UpdatedAt,
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
			gList := groupsByTicket[c.ID]
			if gList == nil {
				gList = []LinkedGroupDTO{}
			}
			dto.LinkedGroups = gList
		}
		out[i] = dto
	}
	return out, nil
}

// loadVisibleLinkedTargets 批量加载工单 ticketIDs 下当前成员 sub 有权限查看的受影响联系人与外部群摘要。
// 对受限成员（ScopeTeam/ScopeAssigned）严格校验是否可见该对象的服务型会话，防止不同团队或代理之间泄露权限之外的对象。
func loadVisibleLinkedTargets(ctx context.Context, db bun.IDB, sub identity.Subject, ticketIDs []string) (map[string][]LinkedContactDTO, map[string][]LinkedGroupDTO, error) {
	contactRes := make(map[string][]LinkedContactDTO, len(ticketIDs))
	groupRes := make(map[string][]LinkedGroupDTO, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return contactRes, groupRes, nil
	}
	var links []TicketTarget
	if err := db.NewSelect().Model(&links).
		Where("tenant_id = ? AND ticket_id IN (?)", sub.Tenant, bun.In(ticketIDs)).
		OrderExpr("(source_conversation_id IS NOT NULL) DESC, created_at ASC, id ASC").Scan(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "load ticket targets")
	}
	if len(links) == 0 {
		return contactRes, groupRes, nil
	}

	contactIDSet := map[string]struct{}{}
	var contactIDs []string
	groupIDSet := map[string]struct{}{}
	var groupIDs []string
	for _, l := range links {
		if l.ContactID != nil && *l.ContactID != "" {
			if _, dup := contactIDSet[*l.ContactID]; !dup {
				contactIDSet[*l.ContactID] = struct{}{}
				contactIDs = append(contactIDs, *l.ContactID)
			}
		}
		if l.ExternalGroupID != nil && *l.ExternalGroupID != "" {
			if _, dup := groupIDSet[*l.ExternalGroupID]; !dup {
				groupIDSet[*l.ExternalGroupID] = struct{}{}
				groupIDs = append(groupIDs, *l.ExternalGroupID)
			}
		}
	}

	seeAll := sub.Type == identity.System || auth.ScopeOf(sub) == auth.ScopeAll
	var chosenConvs []Conversation
	convByID := map[string]Conversation{}

	// 1. 解析受影响联系人的可见服务型会话
	chosenByContact := map[string]string{}
	contactByID := map[string]contact.Summary{}
	if len(contactIDs) > 0 {
		var convs []Conversation
		q := db.NewSelect().Model(&convs).
			Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.contact_id IN (?)", sub.Tenant, Service, bun.In(contactIDs)).
			OrderExpr("(conversation.status IN ('open', 'snoozed')) DESC, conversation.last_message_at DESC, conversation.id DESC")
		q = visible(q, sub)
		if err := q.Scan(ctx); err != nil {
			return nil, nil, errors.Wrap(err, "load visible service conversations for ticket contacts")
		}
		convsByContact := map[string][]Conversation{}
		for _, c := range convs {
			if c.ContactID == nil {
				continue
			}
			convsByContact[*c.ContactID] = append(convsByContact[*c.ContactID], c)
			convByID[c.ID] = c
		}
		var visibleContactIDs []string
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
		if len(visibleContactIDs) > 0 {
			var contactRows []contact.Contact
			if err := db.NewSelect().Model(&contactRows).Where("tenant_id = ? AND id IN (?)", sub.Tenant, bun.In(visibleContactIDs)).Scan(ctx); err != nil {
				return nil, nil, errors.Wrap(err, "load contacts for ticket links")
			}
			for i := range contactRows {
				contactByID[contactRows[i].ID] = contactRows[i].Summary()
			}
		}
	}

	// 2. 解析受影响外部群的可见服务型会话
	chosenByGroup := map[string]string{}
	groupByID := map[string]extgroup.Summary{}
	if len(groupIDs) > 0 {
		var convs []Conversation
		q := db.NewSelect().Model(&convs).
			Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.external_group_id IN (?)", sub.Tenant, Service, bun.In(groupIDs)).
			OrderExpr("(conversation.status IN ('open', 'snoozed')) DESC, conversation.last_message_at DESC, conversation.id DESC")
		q = visible(q, sub)
		if err := q.Scan(ctx); err != nil {
			return nil, nil, errors.Wrap(err, "load visible service conversations for ticket groups")
		}
		convsByGroup := map[string][]Conversation{}
		for _, c := range convs {
			if c.ExternalGroupID == nil {
				continue
			}
			convsByGroup[*c.ExternalGroupID] = append(convsByGroup[*c.ExternalGroupID], c)
			convByID[c.ID] = c
		}
		var visibleGroupIDs []string
		for _, gid := range groupIDs {
			list := convsByGroup[gid]
			if !seeAll && len(list) == 0 {
				continue
			}
			visibleGroupIDs = append(visibleGroupIDs, gid)
			if len(list) > 0 {
				chosenByGroup[gid] = list[0].ID
				chosenConvs = append(chosenConvs, list[0])
			}
		}
		if len(visibleGroupIDs) > 0 {
			var groupRows []extgroup.Group
			if err := db.NewSelect().Model(&groupRows).Where("tenant_id = ? AND id IN (?)", sub.Tenant, bun.In(visibleGroupIDs)).Scan(ctx); err != nil {
				return nil, nil, errors.Wrap(err, "load external groups for ticket links")
			}
			for i := range groupRows {
				groupByID[groupRows[i].ID] = groupRows[i].Summary()
			}
		}
	}

	convDTOByID := map[string]ConversationDTO{}
	if len(chosenConvs) > 0 {
		dtos, err := toDTOs(ctx, db, sub, chosenConvs)
		if err != nil {
			return nil, nil, err
		}
		for _, d := range dtos {
			convDTOByID[d.ID] = d
		}
	}

	for _, l := range links {
		if l.ContactID != nil && *l.ContactID != "" {
			ct, ok := contactByID[*l.ContactID]
			if !ok {
				continue
			}
			item := LinkedContactDTO{
				Contact:  ct,
				IsSource: l.SourceConversationID != nil && *l.SourceConversationID != "",
				LinkedAt: l.CreatedAt,
			}
			if convID := chosenByContact[*l.ContactID]; convID != "" {
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
			contactRes[l.TicketID] = append(contactRes[l.TicketID], item)
		} else if l.ExternalGroupID != nil && *l.ExternalGroupID != "" {
			g, ok := groupByID[*l.ExternalGroupID]
			if !ok {
				continue
			}
			item := LinkedGroupDTO{
				Group:    g,
				IsSource: l.SourceConversationID != nil && *l.SourceConversationID != "",
				LinkedAt: l.CreatedAt,
			}
			if convID := chosenByGroup[*l.ExternalGroupID]; convID != "" {
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
			groupRes[l.TicketID] = append(groupRes[l.TicketID], item)
		}
	}
	return contactRes, groupRes, nil
}

// resumeWaitingTickets 在外部方往服务型会话 c 发来新消息的同一事务中，
// 将关联到此联系人或外部群、且正处于 waiting_on_customer 状态的工单自动切回 in_progress 并记系统事件。
func resumeWaitingTickets(ctx context.Context, tx database.Tx, down Downstream, tenantID string, c *Conversation, _ string, at time.Time) error {
	var ticketIDs []string
	q := tx.NewSelect().TableExpr("ticket_targets tc").
		Join("JOIN conversations conv ON conv.id = tc.ticket_id").
		ColumnExpr("DISTINCT conv.id").
		Where("tc.tenant_id = ? AND conv.tenant_id = ? AND conv.kind = ? AND conv.status = ?",
			tenantID, tenantID, Ticket, TicketWaiting)
	switch {
	case c.ContactID != nil && *c.ContactID != "":
		q = q.Where("tc.contact_id = ?", *c.ContactID)
	case c.ExternalGroupID != nil && *c.ExternalGroupID != "":
		q = q.Where("tc.external_group_id = ?", *c.ExternalGroupID)
	default:
		return nil
	}
	if err := q.OrderExpr("conv.id ASC").Scan(ctx, &ticketIDs); err != nil {
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

// derivedMap 返回 derived[key] 对应的 map；不存在或类型不匹配时返回 nil。
func derivedMap(derived map[string]any, key string) map[string]any {
	if derived == nil {
		return nil
	}
	m, _ := derived[key].(map[string]any)
	return m
}

// translationInMap 从按语言索引的译文 map 中取出匹配 target（含 BCP 47 等价标签）的非空译文。
func translationInMap(m map[string]any, target string) string {
	if len(m) == 0 || target == "" {
		return ""
	}
	if v, ok := m[target].(string); ok && v != "" {
		return v
	}
	for k, raw := range m {
		if v, ok := raw.(string); ok && v != "" && lang.Same(k, target) {
			return v
		}
	}
	return ""
}

// TicketMetaMissingLanguages 返回 targets 中工单标题/描述还缺译文的语言列表。
func TicketMetaMissingLanguages(title, desc string, derived map[string]any, targets []string) []string {
	if title == "" && desc == "" {
		return nil
	}
	source, _ := derived["language"].(string)
	if source == Undetermined {
		return nil
	}
	if source == "" {
		return targets
	}
	titles := derivedMap(derived, "title")
	descs := derivedMap(derived, "description")
	var missing []string
	for _, t := range targets {
		if t == "" || lang.Same(source, t) {
			continue
		}
		needTitle := title != "" && translationInMap(titles, t) == ""
		needDesc := desc != "" && translationInMap(descs, t) == ""
		if needTitle || needDesc {
			missing = append(missing, t)
		}
	}
	return missing
}

// SaveTicketMetaTranslation 在事务中合并保存工单 ticketID 标题与描述的源语言和译文，
// 提交后向可见成员及客户可见工单的网页聊天插件推送刷新事件。
func SaveTicketMetaTranslation(ctx context.Context, db *bun.DB, down Downstream, tenantID, ticketID, source string, titles, descs map[string]string) error {
	return database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := find(ctx, tx, tenantID, ticketID, true)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil || c.Kind != Ticket {
			return err
		}
		var tk TicketMeta
		err = tx.NewSelect().Model(&tk).Where("conversation_id = ? AND tenant_id = ?", ticketID, tenantID).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "load ticket meta for translation")
		}
		if tk.Derived == nil {
			tk.Derived = map[string]any{}
		}
		changed := false
		if cur, _ := tk.Derived["language"].(string); cur == "" && source != "" {
			tk.Derived["language"] = source
			changed = true
		}
		titleMap := derivedMap(tk.Derived, "title")
		if titleMap == nil {
			titleMap = map[string]any{}
		}
		for k, v := range titles {
			if prev, _ := titleMap[k].(string); prev != v {
				titleMap[k] = v
				changed = true
			}
		}
		if len(titleMap) > 0 {
			tk.Derived["title"] = titleMap
		}
		descMap := derivedMap(tk.Derived, "description")
		if descMap == nil {
			descMap = map[string]any{}
		}
		for k, v := range descs {
			if prev, _ := descMap[k].(string); prev != v {
				descMap[k] = v
				changed = true
			}
		}
		if len(descMap) > 0 {
			tk.Derived["description"] = descMap
		}
		if !changed {
			return nil
		}
		if _, err := tx.NewUpdate().Model(&tk).Column("derived").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save ticket meta translation")
		}
		ev := push.Event{Type: push.Ticket, ID: c.ID, Seq: c.LastSeq}
		if down.Hub != nil {
			audience, err := audienceOf(ctx, tx, down.Roles, c, nil)
			if err != nil {
				return err
			}
			if len(audience) > 0 {
				hub := down.Hub
				tx.AfterCommit(func(ctx context.Context) error {
					return errors.Wrap(push.Send(ctx, hub, tenantID, audience, ev), "push ticket meta translation")
				})
			}
		}
		if down.ContactPush != nil && tk.Visibility == tickettype.VisibilityAll {
			targets, err := ticketContactPushTargets(ctx, tx, down, c, []*Item{{Kind: Event, Content: Content{Event: "ticket.updated"}}})
			if err != nil {
				return err
			}
			if len(targets) > 0 {
				notify := down.ContactPush
				tx.AfterCommit(func(ctx context.Context) error {
					var errs []error
					for _, target := range targets {
						if err := notify(ctx, target.ChannelID, target.ContactID, ev); err != nil {
							errs = append(errs, errors.Wrap(err, "push ticket meta translation to contact"))
						}
					}
					return errors.Join(errs...)
				})
			}
		}
		return nil
	})
}

// VisibleTicketIDs 返回 ids 中属于 sub 所在工作区、sub 能看到的工单会话 ID 列表。
func VisibleTicketIDs(ctx context.Context, db bun.IDB, sub identity.Subject, ids []string) ([]string, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var out []string
	q := visible(db.NewSelect().Model((*Conversation)(nil)).Column("conversation.id").
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.id IN (?)", sub.Tenant, Ticket, bun.In(ids)), sub)
	err := q.Scan(ctx, &out)
	return out, errors.Wrap(err, "load visible ticket ids")
}
