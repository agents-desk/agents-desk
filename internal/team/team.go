// Package team 是工作区中的团队：一组成员，其中可以有几位组长。以后工单按团队进入队列、按团队的工作时间计算 SLA，
// 网页插件有默认的团队。团队由主管理员和管理员管理；只有组长、管理员和主管理员能当团队的组长。
package team

import (
	"context"
	"database/sql"
	"slices"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/auth"
)

var (
	ErrNotFound  = errorx.Define(errorx.NotFound, "team.not_found")
	ErrNameTaken = errorx.Define(errorx.Conflict, "team.name_taken")
	// ErrLeadRole 是要把坐席设为团队的组长：只有组长、管理员和主管理员能当团队的组长。
	ErrLeadRole = errorx.Define(errorx.InvalidArgument, "team.lead_role_required")
	// ErrNotInTeam 是要移出的人不在这个团队中。
	ErrNotInTeam = errorx.Define(errorx.NotFound, "team.not_in_team")
)

// Team 是一个团队，属于一个工作区，名称在工作区中不重复。
type Team struct {
	bun.BaseModel `bun:"table:teams"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID    string `bun:"tenant_id,notnull"`
	Name        string `bun:"name,notnull"`
	Description string `bun:"description,notnull"`
}

// Membership 是团队中的一个成员：Lead 表示他是这个团队的组长。
type Membership struct {
	bun.BaseModel `bun:"table:team_members"`

	TeamID   string `bun:"team_id,pk"`
	MemberID string `bun:"member_id,pk"`
	Lead     bool   `bun:"lead,notnull"`
	database.Timestamps
}

// TeamDTO 是接口中的团队，带有人数和组长的名字。
type TeamDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MemberCount int      `json:"member_count" doc:"团队中的人数"`
	Leads       []string `json:"leads" doc:"组长的名字"`
}

// TeamOutput 是返回一个团队的响应。
type TeamOutput struct {
	Body TeamDTO
}

// IDInput 是指定一个团队的请求：路径中的团队 ID。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// canLead 报告工作区角色 role 能不能当团队的组长：组长、管理员和主管理员能，坐席不能。
func canLead(role auth.RoleName) bool { return role != auth.RoleAgent }

// find 读取工作区 tenant 中的团队 id，不存在或不属于这个工作区时返回 ErrNotFound。lock 为 true 时锁住这一行。
func find(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*Team, error) {
	t := new(Team)
	q := db.NewSelect().Model(t).Where("id = ? AND tenant_id = ?", id, tenant)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return t, errors.Wrap(err, "load team")
}

// toDTOs 把团队转换为接口中的团队：一次查出各团队的人数和组长的名字。
func toDTOs(ctx context.Context, db bun.IDB, teams []Team) ([]TeamDTO, error) {
	out := make([]TeamDTO, len(teams))
	if len(teams) == 0 {
		return out, nil
	}
	ids := make([]string, len(teams))
	for i, t := range teams {
		ids[i] = t.ID
		out[i] = TeamDTO{ID: t.ID, Name: t.Name, Description: t.Description, Leads: []string{}}
	}
	var rows []struct {
		TeamID string `bun:"team_id"`
		Name   string `bun:"name"`
		Lead   bool   `bun:"lead"`
	}
	err := db.NewRaw(`SELECT tm.team_id, m.name, tm.lead FROM team_members tm JOIN members m ON m.id = tm.member_id
		WHERE tm.team_id IN (?) ORDER BY m.name`, bun.In(ids)).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load team members")
	}
	for _, r := range rows {
		i := slices.Index(ids, r.TeamID)
		out[i].MemberCount++
		if r.Lead {
			out[i].Leads = append(out[i].Leads, r.Name)
		}
	}
	return out, nil
}

// OnRoleChanged 是 member.Hooks 的 RoleChanged：成员的角色换成坐席后，撤掉他在这个工作区所有团队中的组长身份。
func OnRoleChanged(ctx context.Context, tx bun.IDB, tenant, memberID string, role auth.RoleName) error {
	if canLead(role) {
		return nil
	}
	_, err := tx.NewUpdate().Model((*Membership)(nil)).Set("lead = false").
		Where("member_id = ? AND lead AND team_id IN (SELECT id FROM teams WHERE tenant_id = ?)", memberID, tenant).Exec(ctx)
	return errors.Wrap(err, "clear team leads")
}

// OnMemberRemoved 是 member.Hooks 的 Removed：成员被移出工作区后，把他从这个工作区的团队中删除，别的工作区的团队不受影响。
func OnMemberRemoved(ctx context.Context, tx bun.IDB, tenant, memberID string) error {
	_, err := tx.NewDelete().Model((*Membership)(nil)).
		Where("member_id = ? AND team_id IN (SELECT id FROM teams WHERE tenant_id = ?)", memberID, tenant).Exec(ctx)
	return errors.Wrap(err, "remove member from teams")
}
