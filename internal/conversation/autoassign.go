package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/member"
)

// ErrAlreadyAssigned 是要领取的会话已经被别人领取了：两个人同时领取时，先到者生效。
var ErrAlreadyAssigned = errorx.Define(errorx.Conflict, "conversation.already_assigned")

// AssignReason 是会话的负责人为什么变化，写在 conversation.assigned 系统事件的数据中。
type AssignReason string

const (
	ReasonManual     AssignReason = "manual"      // 成员手动指派
	ReasonDefault    AssignReason = "default"     // 工作区默认指派
	ReasonRoundRobin AssignReason = "round_robin" // 团队收件箱轮流分配
	ReasonReply      AssignReason = "reply"       // 回复后领取
	ReasonAway       AssignReason = "away"        // 负责人离开并选了重新分配，对方再发来消息
	ReasonMemberLeft AssignReason = "member_left" // 负责人离开了工作区
)

// system 是自动规则的发起者：工作区 tenant 中的系统。
func system(tenant string) identity.Subject {
	return identity.Subject{Type: identity.System, ID: "assignment", Tenant: tenant}
}

// assignedEvent 返回 sub 把会话 c 的负责人从 from 换成 c 现在的负责人的系统事件，团队收件箱从 fromTeam 换成 c 现在的。
func assignedEvent(sub identity.Subject, c *Conversation, fromTeam *string, fromType ActorType, fromID string, reason AssignReason) *Item {
	return event(sub, "conversation.assigned", map[string]any{
		"from_team_id": fromTeam, "team_id": c.TeamID,
		"from_assignee": actorData(fromType, fromID), "assignee": actorData(c.AssigneeType, c.AssigneeID),
		"reason": string(reason),
	})
}

// roundRobin 在事务 tx 中给已锁住的会话 c 轮流分配负责人：c 在一个分配方式为轮流的团队收件箱中、并且没有负责人时，
// 分给团队收件箱中在线、最久没有分到会话的成员，返回系统事件；条件不满足或者没有人在线时返回 nil，会话留在团队收件箱。
// 先锁住团队收件箱这一行，同一个团队收件箱的分配依次进行，同时到达的会话不会分给同一个人。
func roundRobin(ctx context.Context, tx bun.IDB, c *Conversation) (*Item, error) {
	if c.TeamID == nil || c.AssigneeType != "" || c.Status == Closed {
		return nil, nil
	}
	var teamRow struct {
		Assignment string     `bun:"assignment"`
		DisabledAt *time.Time `bun:"disabled_at"`
	}
	err := tx.NewRaw("SELECT assignment, disabled_at FROM teams WHERE id = ? AND tenant_id = ? FOR UPDATE", *c.TeamID, c.TenantID).Scan(ctx, &teamRow)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (teamRow.DisabledAt != nil || teamRow.Assignment != "round_robin") {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "lock team")
	}
	var next string
	err = tx.NewRaw(`SELECT tm.member_id FROM team_members tm
		JOIN member_statuses s ON s.tenant_id = ? AND s.member_id = tm.member_id AND s.status = 'online'
		WHERE tm.team_id = ? ORDER BY tm.last_assigned_at NULLS FIRST, tm.member_id LIMIT 1`, c.TenantID, *c.TeamID).Scan(ctx, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "pick round robin member")
	}
	if _, err := tx.NewRaw("UPDATE team_members SET last_assigned_at = ? WHERE team_id = ? AND member_id = ?", now(), *c.TeamID, next).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "record round robin")
	}
	c.AssigneeType, c.AssigneeID = MemberActor, next
	return assignedEvent(system(c.TenantID), c, c.TeamID, "", "", ReasonRoundRobin), nil
}

// releaseIfAway 在事务 tx 中处理对方给已锁住的会话 c 发来新消息时的“离开时重新分配”：负责人处于离开、并且选了把他的会话
// 重新分配时，清空负责人，会话回到团队收件箱，再按团队收件箱的分配方式分配。返回要追加的系统事件，没有变化时为空。
func releaseIfAway(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
	if c.AssigneeType != MemberActor {
		return nil, nil
	}
	var away bool
	err := tx.NewRaw("SELECT status = 'away' AND reassign FROM member_statuses WHERE tenant_id = ? AND member_id = ?", c.TenantID, c.AssigneeID).Scan(ctx, &away)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "load assignee status")
	}
	if !away {
		return nil, nil
	}
	return release(ctx, tx, c, ReasonAway)
}

// release 在事务 tx 中清空已锁住的会话 c 的负责人，会话留在团队收件箱，再按团队收件箱的分配方式分配。返回要追加的系统事件。
func release(ctx context.Context, tx bun.IDB, c *Conversation, reason AssignReason) ([]*Item, error) {
	fromType, fromID := c.AssigneeType, c.AssigneeID
	c.AssigneeType, c.AssigneeID = "", ""
	items := []*Item{assignedEvent(system(c.TenantID), c, c.TeamID, fromType, fromID, reason)}
	next, err := roundRobin(ctx, tx, c)
	if err != nil || next == nil {
		return items, err
	}
	return append(items, next), nil
}

// OnMemberLeft 返回 member.Hooks 中“离开工作区之后”的一步：成员离开工作区 tenant 后，他负责的未关闭会话放回团队收件箱，
// 按团队收件箱的分配方式重新分配，时间线中追加系统事件（reason 是 member_left）。已关闭的会话保留原来的负责人，报表要用。
// 成员离开时他的在线状态已经删除，轮流分配不会再分给他。down 是时间线有新内容时通知的下游。
func OnMemberLeft(down Downstream) member.RemovedHook {
	return func(ctx context.Context, tx database.Tx, tenant, memberID string) error {
		var convs []Conversation
		err := tx.NewSelect().Model(&convs).
			Where("tenant_id = ? AND assignee_type = ? AND assignee_id = ? AND status <> ?", tenant, MemberActor, memberID, Closed).
			OrderExpr("id").For("UPDATE").Scan(ctx)
		if err != nil {
			return errors.Wrap(err, "load conversations of leaving member")
		}
		for i := range convs {
			items, err := release(ctx, tx, &convs[i], ReasonMemberLeft)
			if err != nil {
				return err
			}
			if err := commit(ctx, tx, down, &convs[i], items...); err != nil {
				return err
			}
		}
		// 若工作区默认指派目标正是离开的成员，自动重置为不指派
		group := NewAssignmentSettingsGroup(tx).Tenant(tenant)
		cfg, err := group.Get(ctx)
		if err != nil {
			return err
		}
		if cfg.DefaultTarget == DefaultMember && cfg.DefaultMemberID == memberID {
			if err := group.Update(ctx, func(s *AssignmentSettings) error {
				*s = AssignmentSettings{DefaultTarget: DefaultNone}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	}
}
