package conversation

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tickettype"
)

// CreateTicketParams 是新建工单（直接创建或由服务型会话转成）的请求体。
type CreateTicketParams struct {
	TypeID               string                `json:"type_id" format:"uuid" doc:"工单类型 ID"`
	Title                string                `json:"title" minLength:"1" maxLength:"120" doc:"工单标题"`
	Description          string                `json:"description,omitempty" maxLength:"5000" doc:"工单详细描述"`
	Visibility           tickettype.Visibility `json:"visibility,omitempty" doc:"工单可见范围（all 或 members）；不填取工单类型默认值"`
	Priority             Priority              `json:"priority,omitempty" doc:"优先级；不填默认为 normal"`
	TeamID               *string               `json:"team_id,omitempty" format:"uuid" doc:"归属团队收件箱 ID；不填取工单类型默认团队"`
	AssigneeID           *string               `json:"assignee_id,omitempty" format:"uuid" doc:"负责人成员 ID；可为空"`
	SourceConversationID *string               `json:"source_conversation_id,omitempty" format:"uuid" doc:"可选：由哪条服务型会话转成工单"`
	ConversationIDs      []string              `json:"conversation_ids,omitempty" maxItems:"50" uniqueItems:"true" doc:"可选：创建时一并关联的其他服务型会话 ID"`
	Attributes           map[string]any        `json:"attributes,omitempty" doc:"工单类型对应的自定义字段初始值"`
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

// Handle 在 sub 所在的工作区中创建工单：校验工单类型未停用、自定义字段合法、关联服务型会话对 sub 可见，
// 在单个事务内分配递增编号、写入 conversations(kind=ticket)、tickets、conversation_links 及两端系统事件。
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
		tt, err := tickettype.FindActive(ctx, tx, sub.Tenant, p.TypeID)
		if err != nil {
			return err
		}
		vis := p.Visibility
		if vis == "" {
			vis = tt.DefaultVisibility
		}

		// 确定归属团队收件箱：显式传入时校验未停用；未传时继承工单类型默认团队（若未停用）
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
		} else if p.TeamID == nil && tt.DefaultTeamID != nil && *tt.DefaultTeamID != "" {
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
			if _, err := member.RoleOf(ctx, authz.NewStore(tx, auth.Catalog), ref, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return member.ErrNotFound.New(errorx.Field("assignee_id"))
				}
				return err
			}
			assigneeType, assigneeID = MemberActor, *p.AssigneeID
		}

		// 校验初始自定义字段
		attrs, _, err := attribute.ApplyTicketPatch(ctx, tx, sub.Tenant, tt.ID, map[string]any{}, p.Attributes)
		if err != nil {
			return err
		}

		// 校验并锁住所有需要关联的服务型会话（按 ID 排序加锁避免死锁）
		type linkTarget struct {
			id       string
			relation LinkRelation
		}
		seenLinks := map[string]struct{}{}
		var targets []linkTarget
		if p.SourceConversationID != nil && *p.SourceConversationID != "" {
			seenLinks[*p.SourceConversationID] = struct{}{}
			targets = append(targets, linkTarget{id: *p.SourceConversationID, relation: LinkSource})
		}
		for _, cid := range p.ConversationIDs {
			if cid == "" {
				continue
			}
			if _, dup := seenLinks[cid]; dup {
				continue
			}
			seenLinks[cid] = struct{}{}
			targets = append(targets, linkTarget{id: cid, relation: LinkLinked})
		}
		linkedConvs := make([]*Conversation, 0, len(targets))
		for _, tgt := range targets {
			sc, err := findVisible(ctx, tx, sub, tgt.id, true)
			if err != nil {
				return err
			}
			if sc.Kind != Service {
				return ErrWrongKind.New()
			}
			linkedConvs = append(linkedConvs, sc)
		}

		// 在写入末段分配递增编号，尽量缩短对 tenants 行的锁持有时间
		num, err := nextTicketNumber(ctx, tx, sub.Tenant)
		if err != nil {
			return err
		}
		at := now()
		c := &Conversation{
			ID:            newID(),
			TenantID:      sub.Tenant,
			Kind:          Ticket,
			TeamID:        teamID,
			AssigneeType:  assigneeType,
			AssigneeID:    assigneeID,
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
		tk := &TicketMeta{
			ConversationID: c.ID,
			TenantID:       sub.Tenant,
			Number:         num,
			TypeID:         tt.ID,
			Title:          title,
			Description:    desc,
			Visibility:     vis,
			CreatedBy:      createdBy,
		}
		if _, err := tx.NewInsert().Model(tk).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert ticket meta")
		}

		// 建立工单与服务型会话的关联关系，并在各关联服务型会话写入系统事件
		for i, tgt := range targets {
			link := &ConversationLink{
				ID:             newID(),
				TenantID:       sub.Tenant,
				TicketID:       c.ID,
				ConversationID: tgt.id,
				Relation:       tgt.relation,
				CreatedBy:      createdBy,
			}
			if _, err := tx.NewInsert().Model(link).Exec(ctx); err != nil {
				return errors.Wrap(err, "insert conversation link")
			}
			evName := "conversation.ticket_linked"
			if tgt.relation == LinkSource {
				evName = "conversation.ticket_created"
			}
			sc := linkedConvs[i]
			scEv := event(sub, evName, map[string]any{
				"ticket_id": c.ID,
				"number":    num,
				"title":     title,
			})
			if err := commit(ctx, tx, a.down, sc, scEv); err != nil {
				return err
			}
		}

		// 未指定负责人时，按默认团队收件箱的轮流分配规则自动分派
		items := []*Item{
			event(sub, "ticket.created", map[string]any{
				"number":     num,
				"type_id":    tt.ID,
				"type_name":  tt.Name,
				"title":      title,
				"visibility": string(vis),
			}),
		}
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

// HTTP 处理 POST /t/{slug}/api/tickets。
func (a *CreateTicket) HTTP(ctx context.Context, in *CreateTicketInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}
