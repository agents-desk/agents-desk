package conversation

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
	"agents-desk/internal/extgroup"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tickettype"
)

// CreateTicketParams 是新建工单（直接创建或由服务型会话转成）的请求体。
type CreateTicketParams struct {
	TypeID                *string               `json:"type_id,omitempty" format:"uuid" doc:"可选：工单类型 ID；不选则为通用工单"`
	Title                 string                `json:"title" minLength:"1" maxLength:"120" doc:"工单标题"`
	Description           string                `json:"description,omitempty" maxLength:"5000" doc:"工单详细描述"`
	Visibility            tickettype.Visibility `json:"visibility,omitempty" doc:"工单可见范围（all 或 members）；不填取工单类型默认值或 all"`
	Priority              Priority              `json:"priority,omitempty" doc:"优先级；不填默认为 normal"`
	TeamID                *string               `json:"team_id,omitempty" format:"uuid" doc:"归属团队收件箱 ID；不填取工单类型默认团队"`
	AssigneeID            *string               `json:"assignee_id,omitempty" format:"uuid" doc:"负责人成员 ID；可为空"`
	SourceConversationID  *string               `json:"source_conversation_id,omitempty" format:"uuid" doc:"可选：由哪条服务型会话转成工单，自动关联该会话的联系人或外部群"`
	SourceConversationIDs []string              `json:"source_conversation_ids,omitempty" maxItems:"50" uniqueItems:"true" doc:"可选：同时由多条服务型会话一起转成此工单，自动关联各会话对象并在会话时间线留痕"`
	ContactIDs            []string              `json:"contact_ids,omitempty" maxItems:"50" uniqueItems:"true" doc:"可选：创建时一并关联的受影响联系人 ID"`
	ExternalGroupIDs      []string              `json:"external_group_ids,omitempty" maxItems:"50" uniqueItems:"true" doc:"可选：创建时一并关联的受影响外部群 ID"`
	Attributes            map[string]any        `json:"attributes,omitempty" doc:"工单类型对应的自定义字段初始值"`
}

// CreateTicketInput 是新建工单的 HTTP 请求。
type CreateTicketInput struct {
	Body CreateTicketParams
}

// CreateTicket 在当前工作区中新建一张工单（直接创建或由会话转成）。
type CreateTicket struct {
	db   *bun.DB
	down Downstream
}

// NewCreateTicket 返回新建工单的动作；down 是时间线写入后通知的下游。
func NewCreateTicket(db *bun.DB, down Downstream) *CreateTicket {
	return &CreateTicket{db: db, down: down}
}

// Handle 在 sub 所在的工作区中创建工单：校验可选工单类型未停用、自定义字段合法、来源会话与受影响联系人可见，
// 在单个事务内分配递增编号、写入 conversations(kind=ticket)、tickets、ticket_targets 及系统事件。
func (a *CreateTicket) Handle(ctx context.Context, sub identity.Subject, p CreateTicketParams) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(p.Title)
	desc := strings.TrimSpace(p.Description)
	priority := p.Priority
	if priority == "" {
		priority = Normal
	}

	var convID string
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var tt *tickettype.TicketType
		if p.TypeID != nil && *p.TypeID != "" {
			var err error
			tt, err = tickettype.FindActive(ctx, tx, sub.Tenant, *p.TypeID)
			if err != nil {
				return err
			}
		}
		vis := p.Visibility
		if vis == "" {
			if tt != nil {
				vis = tt.DefaultVisibility
			} else {
				vis = tickettype.VisibilityAll
			}
		}

		// 确定归属团队收件箱：显式传入时校验未停用；未传且选了工单类型时继承类型默认团队（若未停用）
		var teamID *string
		if p.TeamID != nil && *p.TeamID != "" {
			var row struct {
				DisabledAt *time.Time `bun:"disabled_at"`
			}
			err := tx.NewSelect().Table("teams").Column("disabled_at").
				Where("id = ? AND tenant_id = ?", *p.TeamID, sub.Tenant).Scan(ctx, &row)
			if errors.Is(err, sql.ErrNoRows) {
				return team.ErrNotFound.New(errorx.Field("team_id"))
			}
			if err != nil {
				return errors.Wrap(err, "find team for ticket")
			}
			if row.DisabledAt != nil {
				return team.ErrDisabled.New(errorx.Field("team_id"))
			}
			id := *p.TeamID
			teamID = &id
		} else if p.TeamID == nil && tt != nil && tt.DefaultTeamID != nil && *tt.DefaultTeamID != "" {
			active, err := tx.NewSelect().Table("teams").
				Where("id = ? AND tenant_id = ? AND disabled_at IS NULL", *tt.DefaultTeamID, sub.Tenant).Exists(ctx)
			if err != nil {
				return errors.Wrap(err, "check ticket type default team")
			}
			if active {
				id := *tt.DefaultTeamID
				teamID = &id
			}
		}

		// 确定负责人：显式传入时校验属于当前工作区；改派给他人要求有 conversations:assign 权限
		var assigneeType ActorType
		var assigneeID string
		if p.AssigneeID != nil && *p.AssigneeID != "" {
			if sub.Type != identity.System && *p.AssigneeID != sub.ID && !sub.Can(string(auth.ConversationsAssign)) {
				return ErrForbidden.New()
			}
			ref := identity.Ref{Type: member.Type, ID: *p.AssigneeID}
			if _, err := member.RoleOf(ctx, a.down.rolesWithDB(tx), ref, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return member.ErrNotFound.New(errorx.Field("assignee_id"))
				}
				return err
			}
			assigneeType, assigneeID = MemberActor, *p.AssigneeID
		}

		// 校验初始自定义字段：有类型时按类型定义校验，通用工单不接受自定义字段
		attrs := map[string]any{}
		if tt != nil {
			var err error
			attrs, _, err = attribute.ApplyTicketPatch(ctx, tx, sub.Tenant, tt.ID, map[string]any{}, p.Attributes)
			if err != nil {
				return err
			}
		} else if len(p.Attributes) > 0 {
			keys := make([]string, 0, len(p.Attributes))
			for k := range p.Attributes {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return attribute.ErrUnknownKey.New(errorx.Field("attributes."+keys[0]), errorx.Public("key", keys[0]))
		}

		// 解析并校验来源服务型会话（单条或多条）与受影响联系人/外部群列表
		type targetLink struct {
			contactID       *string
			externalGroupID *string
			sourceID        *string
		}
		sourceIDSet := map[string]bool{}
		var sourceIDs []string
		if p.SourceConversationID != nil && *p.SourceConversationID != "" {
			sourceIDSet[*p.SourceConversationID] = true
			sourceIDs = append(sourceIDs, *p.SourceConversationID)
		}
		for _, id := range p.SourceConversationIDs {
			if id == "" || sourceIDSet[id] {
				continue
			}
			sourceIDSet[id] = true
			sourceIDs = append(sourceIDs, id)
		}

		// 按确定顺序锁所有来源会话行，避免并发建单或关联时双向加锁死锁
		sortedSourceIDs := append([]string(nil), sourceIDs...)
		sort.Strings(sortedSourceIDs)
		lockedSources := make(map[string]*Conversation, len(sortedSourceIDs))
		for _, id := range sortedSourceIDs {
			sc, err := findVisible(ctx, tx, sub, id, true)
			if err != nil {
				return err
			}
			if sc.Kind != Service {
				return ErrWrongKind.New()
			}
			lockedSources[id] = sc
		}

		seenContacts := map[string]bool{}
		seenGroups := map[string]bool{}
		var links []targetLink
		sourceConvs := make([]*Conversation, 0, len(sourceIDs))
		for _, id := range sourceIDs {
			sc := lockedSources[id]
			sourceConvs = append(sourceConvs, sc)
			if sc.ContactID != nil && *sc.ContactID != "" && !seenContacts[*sc.ContactID] {
				seenContacts[*sc.ContactID] = true
				cid, sid := *sc.ContactID, sc.ID
				links = append(links, targetLink{contactID: &cid, sourceID: &sid})
			} else if sc.ExternalGroupID != nil && *sc.ExternalGroupID != "" && !seenGroups[*sc.ExternalGroupID] {
				seenGroups[*sc.ExternalGroupID] = true
				gid, sid := *sc.ExternalGroupID, sc.ID
				links = append(links, targetLink{externalGroupID: &gid, sourceID: &sid})
			}
		}
		for _, cid := range p.ContactIDs {
			if cid == "" || seenContacts[cid] {
				continue
			}
			if err := ensureVisibleContact(ctx, tx, sub, cid); err != nil {
				return err
			}
			seenContacts[cid] = true
			cCopy := cid
			links = append(links, targetLink{contactID: &cCopy})
		}
		for _, gid := range p.ExternalGroupIDs {
			if gid == "" || seenGroups[gid] {
				continue
			}
			if err := ensureVisibleGroup(ctx, tx, sub, gid); err != nil {
				return err
			}
			seenGroups[gid] = true
			gCopy := gid
			links = append(links, targetLink{externalGroupID: &gCopy})
		}

		// 在写入末段分配递增编号，缩短对 tenants 行的锁持有时间
		num, err := nextTicketNumber(ctx, tx, sub.Tenant)
		if err != nil {
			return err
		}
		at := database.Now()
		c := &Conversation{
			ID:            database.NewID(),
			TenantID:      sub.Tenant,
			Kind:          Ticket,
			TeamID:        teamID,
			AssigneeType:  assigneeType,
			AssigneeID:    assigneeID,
			HandlingMode:  HandlingHuman,
			Status:        Status(TicketSubmitted),
			Priority:      priority,
			OpenedAt:      at,
			LastMessageAt: at,
			Attributes:    attrs,
		}
		if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert ticket conversation")
		}
		var createdBy *string
		if sub.Type == member.Type {
			id := sub.ID
			createdBy = &id
			if err := join(ctx, tx, c.ID, MemberActor, sub.ID); err != nil {
				return err
			}
		}
		var typeID *string
		var typeName string
		if tt != nil {
			id := tt.ID
			typeID = &id
			typeName = tt.Name
		}
		tk := &TicketMeta{
			ConversationID: c.ID,
			TenantID:       sub.Tenant,
			Number:         num,
			TypeID:         typeID,
			Title:          title,
			Description:    desc,
			Visibility:     vis,
			CreatedBy:      createdBy,
			Derived:        map[string]any{},
		}
		if _, err := tx.NewInsert().Model(tk).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert ticket meta")
		}

		// 写入受影响对象关联，并在每条来源服务型会话记系统事件
		for _, l := range links {
			rec := &TicketTarget{
				ID:                   database.NewID(),
				TenantID:             sub.Tenant,
				TicketID:             c.ID,
				ContactID:            l.contactID,
				ExternalGroupID:      l.externalGroupID,
				SourceConversationID: l.sourceID,
				CreatedBy:            createdBy,
			}
			if _, err := tx.NewInsert().Model(rec).Exec(ctx); err != nil {
				return errors.Wrap(err, "insert ticket target")
			}
		}
		for _, sc := range sourceConvs {
			scEv := event(sub, "conversation.ticket_created", map[string]any{
				"ticket_id": c.ID,
				"number":    num,
				"title":     title,
			})
			if err := commit(ctx, tx, a.down, sc, scEv); err != nil {
				return err
			}
		}

		// 未指定负责人时，按默认团队收件箱的轮流分配规则自动分派
		evData := map[string]any{
			"number":     num,
			"title":      title,
			"visibility": string(vis),
		}
		if typeID != nil {
			evData["type_id"] = *typeID
			evData["type_name"] = typeName
		}
		items := []*Item{event(sub, "ticket.created", evData)}
		if c.AssigneeType == "" {
			if auto, err := roundRobin(ctx, tx, c); err != nil {
				return err
			} else if auto != nil {
				items = append(items, auto)
			}
		}
		if err := commit(ctx, tx, a.down, c, items...); err != nil {
			return err
		}
		convID = c.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewGetTicket(a.db).Handle(ctx, sub, convID)
}

// ensureVisibleContact 校验联系人 contactID 属于 sub 所在工作区；若 sub 属于受限可见范围，还校验其能看到该联系人的至少一条服务型会话。
func ensureVisibleContact(ctx context.Context, db bun.IDB, sub identity.Subject, contactID string) error {
	exists, err := db.NewSelect().Table("contacts").Where("id = ? AND tenant_id = ?", contactID, sub.Tenant).Exists(ctx)
	if err != nil {
		return errors.Wrap(err, "check contact exists")
	}
	if !exists {
		return contact.ErrNotFound.New()
	}
	if sub.Type == identity.System || auth.ScopeOf(sub) == auth.ScopeAll {
		return nil
	}
	q := db.NewSelect().Model((*Conversation)(nil)).
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.contact_id = ?", sub.Tenant, Service, contactID)
	q = visible(q, sub)
	canSee, err := q.Exists(ctx)
	if err != nil {
		return errors.Wrap(err, "check visible contact conversation")
	}
	if !canSee {
		return contact.ErrNotFound.New()
	}
	return nil
}

// ensureVisibleGroup 校验外部群 groupID 属于 sub 所在工作区；若 sub 属于受限可见范围，还校验其能看到该外部群的至少一条服务型会话。
func ensureVisibleGroup(ctx context.Context, db bun.IDB, sub identity.Subject, groupID string) error {
	exists, err := db.NewSelect().Table("external_groups").Where("id = ? AND tenant_id = ?", groupID, sub.Tenant).Exists(ctx)
	if err != nil {
		return errors.Wrap(err, "check external group exists")
	}
	if !exists {
		return extgroup.ErrNotFound.New()
	}
	if sub.Type == identity.System || auth.ScopeOf(sub) == auth.ScopeAll {
		return nil
	}
	q := db.NewSelect().Model((*Conversation)(nil)).
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.external_group_id = ?", sub.Tenant, Service, groupID)
	q = visible(q, sub)
	canSee, err := q.Exists(ctx)
	if err != nil {
		return errors.Wrap(err, "check visible group conversation")
	}
	if !canSee {
		return extgroup.ErrNotFound.New()
	}
	return nil
}

// HTTP 处理 POST /t/{slug}/api/tickets。
func (a *CreateTicket) HTTP(ctx context.Context, in *CreateTicketInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}
