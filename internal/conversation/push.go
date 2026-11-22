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
	before := new(Conversation)
	err := db.NewSelect().Model(before).Column("team_id", "assignee_type", "assignee_id").Where("id = ?", c.ID).Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load conversation before change")
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
		return nil, errors.Wrap(err, "load mentioned members")
	}
	for _, it := range items {
		mentioned = append(mentioned, it.Content.Mentions...)
	}
	for _, id := range mentioned {
		direct[id] = true
	}

	// 修改前后的团队收件箱中的成员
	inTeam := map[string]bool{}
	if len(teams) > 0 {
		var ids []string
		if err := db.NewSelect().Table("team_members").Column("member_id").Where("team_id IN (?)", bun.In(teams)).Scan(ctx, &ids); err != nil {
			return nil, errors.Wrap(err, "load team members")
		}
		for _, id := range ids {
			inTeam[id] = true
		}
	}

	// 按每个成员的角色得出可见范围，权限与 member.Resolve 加载的相同：角色的权限加上基本权限
	members, err := roles.Members(ctx, c.TenantID)
	if err != nil {
		return nil, err
	}
	var out []string
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
		}
	}
	return out, nil
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
		pending = append(pending, item{audience: audience, ev: push.Event{Type: push.Conversation, ID: convs[i].ID, Seq: convs[i].LastSeq}})
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
