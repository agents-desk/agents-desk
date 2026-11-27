package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
)

// audienceOf 返回修改前后能看到会话 c 的成员，规则与 visible 相同：可见范围是全部会话的成员；可见范围是团队收件箱、
// 并且在修改前或修改后的团队收件箱中的成员；修改前或修改后的负责人；在会话中被 @ 过的成员，包括这次追加的 items 中
// @ 到的（它们的记录在追加之后才写入）。看得到修改前的会话的人也要收到推送，他的列表和数量随之变化。
// c 是修改后的会话，修改前的团队收件箱和负责人从数据库中读取，所以要在保存 c 之前调用。
func audienceOf(ctx context.Context, db bun.IDB, roles *authz.Store, c *Conversation, items []*Item) ([]string, error) {
	audience, _, err := audienceAndAlertsOf(ctx, db, roles, c, items)
	return audience, err
}

// audienceAndAlertsOf 在计算可见推送对象（audienceOf）的同时，计算每个成员本次应触发的实时提醒类型：
// 有人在内部备注中 @ 我（mention）、新会话指派给我（assigned）、我负责的会话收到客户新消息（new_message）。
// 成员自己发起的操作不提醒本人；同一事务命中多种提醒时按 mention > assigned > new_message 选主提醒。
func audienceAndAlertsOf(ctx context.Context, db bun.IDB, roles *authz.Store, c *Conversation, items []*Item) ([]string, map[string]string, error) {
	// 内部聊天严格按参与者推送：只推给当前参与者成员，并按新消息与 @提及 生成实时提醒
	if c.Kind == Chat {
		var out []string
		err := db.NewSelect().Table("conversation_participants").Column("participant_id").
			Where("conversation_id = ? AND participant_type = ?", c.ID, MemberActor).
			Scan(ctx, &out)
		if err != nil {
			return nil, nil, errors.Wrap(err, "load chat participants for push")
		}
		visibleSet := make(map[string]bool, len(out))
		for _, id := range out {
			visibleSet[id] = true
		}
		alerts := computeChatAlerts(items, visibleSet)
		return out, alerts, nil
	}

	before := new(Conversation)
	err := db.NewSelect().Model(before).Column("team_id", "assignee_type", "assignee_id").Where("id = ?", c.ID).Scan(ctx)
	if err != nil {
		return nil, nil, errors.Wrap(err, "load conversation before change")
	}

	// 负责人和被 @ 的成员：不论可见范围都能看到
	direct := map[string]bool{}
	var teams []string
	for _, x := range []*Conversation{before, c} {
		if x.AssigneeType == MemberActor {
			direct[x.AssigneeID] = true
		}
		if x.TeamID != nil {
			teams = append(teams, *x.TeamID)
		}
	}
	var mentioned []string
	err = db.NewSelect().Table("conversation_mentions").Column("member_id").
		Where("tenant_id = ? AND conversation_id = ?", c.TenantID, c.ID).Scan(ctx, &mentioned)
	if err != nil {
		return nil, nil, errors.Wrap(err, "load mentioned members")
	}
	for _, it := range items {
		mentioned = append(mentioned, it.Content.Mentions...)
	}
	for _, id := range mentioned {
		direct[id] = true
	}

	// 工单：创建人以及关联对象名下服务型会话的负责人、被 @ 成员、所在团队成员也纳入可见推送范围
	if c.Kind == Ticket {
		var createdBy *string
		if err := db.NewSelect().Table("tickets").Column("created_by").Where("conversation_id = ?", c.ID).Scan(ctx, &createdBy); err == nil && createdBy != nil && *createdBy != "" {
			direct[*createdBy] = true
		}
		var linked []struct {
			TeamID       *string   `bun:"team_id"`
			AssigneeType ActorType `bun:"assignee_type"`
			AssigneeID   string    `bun:"assignee_id"`
		}
		if err := db.NewSelect().TableExpr("ticket_targets tc").
			Join("JOIN conversations sc ON sc.tenant_id = tc.tenant_id AND sc.kind = ? AND ((tc.contact_id IS NOT NULL AND sc.contact_id = tc.contact_id) OR (tc.external_group_id IS NOT NULL AND sc.external_group_id = tc.external_group_id))", Service).
			ColumnExpr("sc.team_id, sc.assignee_type, sc.assignee_id").
			Where("tc.tenant_id = ? AND tc.ticket_id = ?", c.TenantID, c.ID).
			Scan(ctx, &linked); err != nil {
			return nil, nil, errors.Wrap(err, "load linked target conversations for ticket push")
		}
		for _, sc := range linked {
			if sc.AssigneeType == MemberActor && sc.AssigneeID != "" {
				direct[sc.AssigneeID] = true
			}
			if sc.TeamID != nil {
				teams = append(teams, *sc.TeamID)
			}
		}
		var linkedMentioned []string
		if err := db.NewSelect().TableExpr("ticket_targets tc").
			Join("JOIN conversations sc ON sc.tenant_id = tc.tenant_id AND sc.kind = ? AND ((tc.contact_id IS NOT NULL AND sc.contact_id = tc.contact_id) OR (tc.external_group_id IS NOT NULL AND sc.external_group_id = tc.external_group_id))", Service).
			Join("JOIN conversation_mentions cm ON cm.conversation_id = sc.id AND cm.tenant_id = tc.tenant_id").
			ColumnExpr("DISTINCT cm.member_id").
			Where("tc.tenant_id = ? AND tc.ticket_id = ?", c.TenantID, c.ID).
			Scan(ctx, &linkedMentioned); err != nil {
			return nil, nil, errors.Wrap(err, "load linked mentioned members for ticket push")
		}
		for _, id := range linkedMentioned {
			direct[id] = true
		}
	}

	// 修改前后的团队收件箱中的成员
	inTeam := map[string]bool{}
	if len(teams) > 0 {
		var ids []string
		if err := db.NewSelect().Table("team_members").Column("member_id").Where("team_id IN (?)", bun.In(teams)).Scan(ctx, &ids); err != nil {
			return nil, nil, errors.Wrap(err, "load team members")
		}
		for _, id := range ids {
			inTeam[id] = true
		}
	}

	// 按每个成员的角色得出可见范围，权限与 member.Resolve 加载的相同：角色的权限加上基本权限
	members, err := roles.Members(ctx, c.TenantID)
	if err != nil {
		return nil, nil, err
	}
	var out []string
	visibleSet := map[string]bool{}
	for _, m := range members {
		if m.Subject.Type != member.Type {
			continue
		}
		var perms []string
		for _, r := range m.Roles {
			perms = append(perms, r.Permissions...)
		}
		sub := identity.Subject{Type: member.Type, ID: m.Subject.ID, Tenant: c.TenantID, Permissions: auth.WithBase(perms)}
		switch scope := auth.ScopeOf(sub); {
		case scope == auth.ScopeAll, scope == auth.ScopeTeam && inTeam[sub.ID], direct[sub.ID]:
			out = append(out, sub.ID)
			visibleSet[sub.ID] = true
		}
	}

	alerts := computeAlerts(before, c, items, visibleSet)
	return out, alerts, nil
}

// computeAlerts 根据变更前后的会话状态与本次追加的 items，计算需要提醒的成员及其主提醒类型。
// 优先级从低到高依次写入：new_message < assigned < mention，高优先级覆盖低优先级。
func computeAlerts(before, after *Conversation, items []*Item, visible map[string]bool) map[string]string {
	if len(items) == 0 {
		return nil
	}
	alerts := map[string]string{}
	set := func(memberID, alert string) {
		if memberID != "" && visible[memberID] {
			alerts[memberID] = alert
		}
	}

	// 1. 外部用户发来新消息：提醒会话当前负责人
	hasCustomerMsg := false
	for _, it := range items {
		if it.Kind == Message && it.ActorType.IsExternal() {
			hasCustomerMsg = true
			break
		}
	}
	if hasCustomerMsg && after.AssigneeType == MemberActor && after.AssigneeID != "" {
		set(after.AssigneeID, push.AlertNewMessage)
	}

	// 2. 新会话指派给我：负责人变为某成员，且不是他本人主动领取或指派给自己
	assigneeChanged := after.AssigneeType == MemberActor && after.AssigneeID != "" &&
		(before.AssigneeType != MemberActor || before.AssigneeID != after.AssigneeID)
	if assigneeChanged {
		selfAssigned := false
		for _, it := range items {
			if it.Kind == Event && it.Content.Event == "conversation.assigned" && it.ActorType == MemberActor && it.ActorID == after.AssigneeID {
				selfAssigned = true
				break
			}
		}
		if !selfAssigned {
			set(after.AssigneeID, push.AlertAssigned)
		}
	}

	// 3. 有人在内部备注中 @ 我（排除自己 @ 自己）
	for _, it := range items {
		if it.Kind != Note {
			continue
		}
		for _, mid := range it.Content.Mentions {
			if it.ActorType == MemberActor && it.ActorID == mid {
				continue
			}
			set(mid, push.AlertMention)
		}
	}

	if len(alerts) == 0 {
		return nil
	}
	return alerts
}

// computeChatAlerts 计算聊天本次追加的 items 应触发的实时提醒：
// 发消息时提醒除发送者外的其他参与者（new_message），消息或内部备注中被 @ 到的参与者升级为 mention。
func computeChatAlerts(items []*Item, visible map[string]bool) map[string]string {
	if len(items) == 0 {
		return nil
	}
	alerts := map[string]string{}
	for _, it := range items {
		if it.Kind != Message && it.Kind != Note {
			continue
		}
		if it.Kind == Message {
			for memberID := range visible {
				if it.ActorType == MemberActor && it.ActorID == memberID {
					continue
				}
				alerts[memberID] = push.AlertNewMessage
			}
		}
		for _, mid := range it.Content.Mentions {
			if !visible[mid] || (it.ActorType == MemberActor && it.ActorID == mid) {
				continue
			}
			alerts[mid] = push.AlertMention
		}
	}
	if len(alerts) == 0 {
		return nil
	}
	return alerts
}

// NotifyUpdated 在事务 tx 中为会话 ids 登记提交后的实时推送：通知能看到这些会话的成员刷新列表和详情，
// 例如线索登录后合并到用户，会话上的联系人从线索变成了用户。down.Hub 为空或 ids 为空时什么也不做。
func NotifyUpdated(ctx context.Context, tx database.Tx, down Downstream, tenantID string, ids []string) error {
	if down.Hub == nil || len(ids) == 0 {
		return nil
	}
	var convs []Conversation
	if err := tx.NewSelect().Model(&convs).Where("tenant_id = ? AND id IN (?)", tenantID, bun.In(ids)).Scan(ctx); err != nil {
		return errors.Wrap(err, "load conversations for notify")
	}
	type item struct {
		audience []string
		ev       push.Event
	}
	pending := make([]item, 0, len(convs))
	for i := range convs {
		audience, err := audienceOf(ctx, tx, down.Roles, &convs[i], nil)
		if err != nil {
			return err
		}
		if len(audience) == 0 {
			continue
		}
		pending = append(pending, item{audience: audience, ev: push.Event{Type: pushType(convs[i].Kind), ID: convs[i].ID, Seq: convs[i].LastSeq}})
	}
	if len(pending) == 0 {
		return nil
	}
	hub := down.Hub
	tx.AfterCommit(func(ctx context.Context) error {
		var errs []error
		for _, p := range pending {
			if err := push.Send(ctx, hub, tenantID, p.audience, p.ev); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	})
	return nil
}
