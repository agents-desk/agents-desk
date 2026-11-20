// Package team 是工作区中的团队：一组成员。以后作为团队收件箱：会话进入团队收件箱，按团队的工作时间计算回复时间，
// 渠道可以设置默认的团队收件箱。团队由有 teams:manage 权限的成员管理。
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
)

var (
	ErrNotFound  = errorx.Define(errorx.NotFound, "team.not_found")
	ErrNameTaken = errorx.Define(errorx.Conflict, "team.name_taken")
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

// Membership 是团队中的一个成员。
type Membership struct {
	bun.BaseModel `bun:"table:team_members"`

	TeamID   string `bun:"team_id,pk"`
	MemberID string `bun:"member_id,pk"`
	database.Timestamps
}

// TeamDTO 是接口中的团队，带有人数。
type TeamDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MemberCount int    `json:"member_count" doc:"团队中的人数"`
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

// toDTOs 把团队转换为接口中的团队：一次查出各团队的人数。
func toDTOs(ctx context.Context, db bun.IDB, teams []Team) ([]TeamDTO, error) {
	out := make([]TeamDTO, len(teams))
	if len(teams) == 0 {
		return out, nil
	}
	ids := make([]string, len(teams))
	for i, t := range teams {
		ids[i] = t.ID
		out[i] = TeamDTO{ID: t.ID, Name: t.Name, Description: t.Description}
	}
	var rows []struct {
		TeamID string `bun:"team_id"`
		Count  int    `bun:"count"`
	}
	err := db.NewRaw(`SELECT team_id, count(*) AS count FROM team_members WHERE team_id IN (?) GROUP BY team_id`, bun.In(ids)).
		Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "count team members")
	}
	for _, r := range rows {
		out[slices.Index(ids, r.TeamID)].MemberCount = r.Count
	}
	return out, nil
}

// OnMemberRemoved 是 member.Hooks 的 Removed：成员被移出工作区后，把他从这个工作区的团队中删除，别的工作区的团队不受影响。
func OnMemberRemoved(ctx context.Context, tx bun.IDB, tenant, memberID string) error {
	_, err := tx.NewDelete().Model((*Membership)(nil)).
		Where("member_id = ? AND team_id IN (SELECT id FROM teams WHERE tenant_id = ?)", memberID, tenant).Exec(ctx)
	return errors.Wrap(err, "remove member from teams")
}
