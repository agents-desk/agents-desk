package contact

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// memberActor 与 conversation.MemberActor 保持一致：避免 contact 反向依赖 conversation 形成循环导入。
const memberActor = "member"

// visible 给联系人的查询 q 加上 sub 能看到哪些联系人的条件，q 中联系人表的别名是 contact。规则只有这一处，
// 列表、概览计数、读取和修改都经过它：
//   - 系统主体和可见范围是全部（auth.ScopeAll）的成员能看到工作区中的全部联系人；
//   - 可见范围是所在团队收件箱（auth.ScopeTeam）的成员能看到：在自己所在团队绑定的渠道上有身份的联系人，
//     或者拥有自己可见会话（所在团队收件箱、指派给自己、或在内部备注中被 @ 过）的联系人；
//   - 可见范围是仅指派给自己（auth.ScopeAssigned）的成员能看到：拥有指派给自己或在内部备注中被 @ 过的会话的联系人。
func visible(q *bun.SelectQuery, sub identity.Subject) *bun.SelectQuery {
	if sub.Type == identity.System {
		return q
	}
	scope := auth.ScopeOf(sub)
	if scope == auth.ScopeAll {
		return q
	}
	return q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
		if scope == auth.ScopeTeam {
			return q.Where(
				`EXISTS (
					SELECT 1 FROM contact_identities ci
					JOIN channels ch ON ch.id = ci.channel_id AND ch.tenant_id = ci.tenant_id
					WHERE ci.tenant_id = contact.tenant_id AND ci.contact_id = contact.id
						AND ch.team_id IN (SELECT team_id FROM team_members WHERE member_id = ?)
				)`,
				sub.ID,
			).WhereOr(
				`EXISTS (
					SELECT 1 FROM conversations conv
					WHERE conv.tenant_id = contact.tenant_id AND conv.contact_id = contact.id
						AND (
							(conv.assignee_type = ? AND conv.assignee_id = ?)
							OR conv.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)
							OR conv.team_id IN (SELECT team_id FROM team_members WHERE member_id = ?)
						)
				)`,
				memberActor, sub.ID, sub.Tenant, sub.ID, sub.ID,
			)
		}
		return q.Where(
			`EXISTS (
				SELECT 1 FROM conversations conv
				WHERE conv.tenant_id = contact.tenant_id AND conv.contact_id = contact.id
					AND (
						(conv.assignee_type = ? AND conv.assignee_id = ?)
						OR conv.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)
					)
			)`,
			memberActor, sub.ID, sub.Tenant, sub.ID,
		)
	})
}

// findVisible 读取 sub 所在工作区中 sub 能看到的联系人 id；不存在、属于别的工作区或者看不到时都返回 ErrNotFound，
// 不暴露联系人是否存在。lock 为 true 时锁住这一行。
func findVisible(ctx context.Context, db bun.IDB, sub identity.Subject, id string, lock bool) (*Contact, error) {
	c := new(Contact)
	q := visible(db.NewSelect().Model(c).Where("contact.id = ? AND contact.tenant_id = ?", id, sub.Tenant), sub)
	if lock {
		q = q.For("UPDATE OF contact")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return c, errors.Wrap(err, "load contact")
}

// visibleConversations 给会话查询 q 加上与 conversation.visible 一致的可见范围条件，q 中会话表的别名是 conv。
// 用于统计联系人的可见会话数（ConversationCount）。
func visibleConversations(q *bun.SelectQuery, sub identity.Subject) *bun.SelectQuery {
	if sub.Type == identity.System {
		return q
	}
	scope := auth.ScopeOf(sub)
	if scope == auth.ScopeAll {
		return q
	}
	return q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
		q = q.Where("conv.assignee_type = ? AND conv.assignee_id = ?", memberActor, sub.ID).
			WhereOr("conv.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)", sub.Tenant, sub.ID)
		if scope == auth.ScopeTeam {
			q = q.WhereOr("conv.team_id IN (SELECT team_id FROM team_members WHERE member_id = ?)", sub.ID)
		}
		return q
	})
}

// visibleChannels 给渠道查询 q 加上 sub 在联系人筛选中能看到的渠道条件，q 中渠道表的别名是 channel：
//   - 系统主体和 auth.ScopeAll 返回工作区中的全部渠道；
//   - auth.ScopeTeam 返回绑定到自己所在团队收件箱的渠道，以及存在自己可见会话的渠道；
//   - auth.ScopeAssigned 只返回存在自己可见会话的渠道。
func visibleChannels(q *bun.SelectQuery, sub identity.Subject) *bun.SelectQuery {
	if sub.Type == identity.System {
		return q
	}
	scope := auth.ScopeOf(sub)
	if scope == auth.ScopeAll {
		return q
	}
	return q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
		if scope == auth.ScopeTeam {
			return q.Where("channel.team_id IN (SELECT team_id FROM team_members WHERE member_id = ?)", sub.ID).
				WhereOr(
					`EXISTS (
						SELECT 1 FROM conversations conv
						WHERE conv.tenant_id = channel.tenant_id AND conv.channel_id = channel.id
							AND (
								(conv.assignee_type = ? AND conv.assignee_id = ?)
								OR conv.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)
								OR conv.team_id IN (SELECT team_id FROM team_members WHERE member_id = ?)
							)
					)`,
					memberActor, sub.ID, sub.Tenant, sub.ID, sub.ID,
				)
		}
		return q.Where(
			`EXISTS (
				SELECT 1 FROM conversations conv
				WHERE conv.tenant_id = channel.tenant_id AND conv.channel_id = channel.id
					AND (
						(conv.assignee_type = ? AND conv.assignee_id = ?)
						OR conv.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)
					)
			)`,
			memberActor, sub.ID, sub.Tenant, sub.ID,
		)
	})
}
