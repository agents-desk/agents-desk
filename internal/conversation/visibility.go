package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// visible 给会话的查询 q 加上 sub 能看到哪些会话的条件，q 中会话表的别名是 conversation。规则只有这一处，
// 列表、数量和单个会话的读写都经过它：
//   - 系统主体能看到全部；
//   - 成员按角色的可见范围（auth.ScopeOf）：全部；所在团队收件箱的和指派给自己的；只有指派给自己的；
//   - 成员在内部备注中被 @ 过的会话也能看到；
//   - 对工单（kind=ticket），工单创建人以及能看到该工单下任意关联服务型会话的成员也能看到。
func visible(q *bun.SelectQuery, sub identity.Subject) *bun.SelectQuery {
	if sub.Type == identity.System {
		return q
	}
	scope := auth.ScopeOf(sub)
	if scope == auth.ScopeAll {
		return q
	}
	return q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
		q = q.Where("conversation.assignee_type = ? AND conversation.assignee_id = ?", MemberActor, sub.ID).
			WhereOr("conversation.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)", sub.Tenant, sub.ID)
		if scope == auth.ScopeTeam {
			q = q.WhereOr("conversation.team_id IN (SELECT team_id FROM team_members WHERE member_id = ?)", sub.ID)
		}
		// 工单额外规则：创建人始终可见；能看到该工单下任意受影响联系人的服务型会话的成员可见
		q = q.WhereOr("conversation.kind = ? AND EXISTS (SELECT 1 FROM tickets tk WHERE tk.conversation_id = conversation.id AND tk.created_by = ?)", Ticket, sub.ID)
		if scope == auth.ScopeTeam {
			q = q.WhereOr(`conversation.kind = ? AND EXISTS (
				SELECT 1 FROM ticket_contacts tc
				JOIN conversations sc ON sc.tenant_id = tc.tenant_id AND sc.kind = ? AND sc.contact_id = tc.contact_id
				WHERE tc.tenant_id = ? AND tc.ticket_id = conversation.id
				AND (
					(sc.assignee_type = ? AND sc.assignee_id = ?)
					OR sc.team_id IN (SELECT team_id FROM team_members WHERE member_id = ?)
					OR sc.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)
				)
			)`, Ticket, Service, sub.Tenant, MemberActor, sub.ID, sub.ID, sub.Tenant, sub.ID)
		} else {
			q = q.WhereOr(`conversation.kind = ? AND EXISTS (
				SELECT 1 FROM ticket_contacts tc
				JOIN conversations sc ON sc.tenant_id = tc.tenant_id AND sc.kind = ? AND sc.contact_id = tc.contact_id
				WHERE tc.tenant_id = ? AND tc.ticket_id = conversation.id
				AND (
					(sc.assignee_type = ? AND sc.assignee_id = ?)
					OR sc.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)
				)
			)`, Ticket, Service, sub.Tenant, MemberActor, sub.ID, sub.Tenant, sub.ID)
		}
		return q
	})
}

// findVisible 读取 sub 所在工作区中 sub 能看到的会话 id；不存在、属于别的工作区或者看不到时都返回 ErrNotFound，
// 不暴露会话是否存在。lock 为 true 时锁住这一行，同一个会话的写入因此依次进行。
func findVisible(ctx context.Context, db bun.IDB, sub identity.Subject, id string, lock bool) (*Conversation, error) {
	c := new(Conversation)
	q := visible(db.NewSelect().Model(c).Where("conversation.id = ? AND conversation.tenant_id = ?", id, sub.Tenant), sub)
	if lock {
		q = q.For("UPDATE OF conversation")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return c, errors.Wrap(err, "load conversation")
}
