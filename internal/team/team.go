// Package team 是工作区中的团队收件箱：一组成员共同处理服务型会话的队列，有自己的分配方式。会话进入团队收件箱，
// 渠道可以设置默认的团队收件箱。团队收件箱由有 teams:manage 权限的成员管理。
// 代码中统一叫 team（表 teams、列 team_id、权限 teams:*），给用户看的文字叫“团队收件箱”。
package team

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/hours"
)

var (
	ErrNotFound  = errorx.Define(errorx.NotFound, "team.not_found")
	ErrNameTaken = errorx.Define(errorx.Conflict, "team.name_taken")
	// ErrNotInTeam 是要移出的人不在这个团队收件箱中。
	ErrNotInTeam = errorx.Define(errorx.NotFound, "team.not_in_team")
	// ErrInUse 是团队收件箱已有会话、历史关闭记录或渠道引用，不能删除，可以改为停用。
	ErrInUse = errorx.Define(errorx.Conflict, "team.in_use")
	// ErrDisabled 是团队收件箱已停用，不能再接收新会话或被选为目标团队收件箱。
	ErrDisabled = errorx.Define(errorx.Conflict, "team.disabled")
)

// Status 是团队收件箱的状态。
type Status string

const (
	Active   Status = "active"   // 正常启用
	Disabled Status = "disabled" // 已停用：停止接收新会话和自动分配，从收件箱侧栏隐藏，历史会话和报表保留名称
)

// Statuses 是团队收件箱的全部状态，显示文字是翻译文件中 team.status.<状态> 的消息。
var Statuses = enum.New("team.status", enum.Item(Active), enum.Item(Disabled))

// Schema 让 OpenAPI 中的 Status 只接受上面的取值，并带有显示文字。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

// Assignment 是团队收件箱的分配方式：新会话进入团队收件箱后怎样指派给成员。
type Assignment string

const (
	Manual     Assignment = "manual"      // 手动：成员自己领取
	RoundRobin Assignment = "round_robin" // 轮流：依次分给在线的成员
)

// Assignments 是全部分配方式，显示文字是翻译文件中 team_assignment.<方式> 的消息。
var Assignments = enum.New("team_assignment", enum.Item(Manual), enum.Item(RoundRobin))

// Schema 让 OpenAPI 中的 Assignment 只接受上面的取值，并带有显示文字。
func (Assignment) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Assignments) }

// Team 是一个团队收件箱，属于一个工作区，名称在工作区中不重复。
type Team struct {
	bun.BaseModel `bun:"table:teams"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID      string          `bun:"tenant_id,notnull"`
	Name          string          `bun:"name,notnull"`
	Description   string          `bun:"description,notnull"`
	Assignment    Assignment      `bun:"assignment,notnull"`        // 分配方式
	BusinessHours *hours.Schedule `bun:"business_hours,type:jsonb"` // 单独设置的工作时间；为空表示使用工作区的
	DisabledAt    *time.Time      `bun:"disabled_at"`               // 停用时间；为空表示启用
}

// Status 返回团队收件箱当前的状态。
func (t *Team) Status() Status {
	if t.DisabledAt != nil {
		return Disabled
	}
	return Active
}

// Membership 是团队收件箱中的一个成员。
type Membership struct {
	bun.BaseModel `bun:"table:team_members"`

	TeamID   string `bun:"team_id,pk"`
	MemberID string `bun:"member_id,pk"`
	database.Timestamps
}

// TeamDTO 是接口中的团队收件箱，带有人数和状态。
type TeamDTO struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Assignment    Assignment      `json:"assignment" doc:"分配方式"`
	Status        Status          `json:"status" doc:"状态：正常或已停用"`
	DisabledAt    *time.Time      `json:"disabled_at,omitempty" doc:"停用时间，正常启用时为空"`
	MemberCount   int             `json:"member_count" doc:"团队收件箱中的人数"`
	BusinessHours *hours.Schedule `json:"business_hours,omitempty" doc:"单独设置的时区和每周的工作时间；为空表示使用工作区的工作时间"`
}

// TeamOutput 是返回一个团队收件箱的响应。
type TeamOutput struct {
	Body TeamDTO
}

// IDInput 是指定一个团队收件箱的请求：路径中的团队收件箱 ID。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// find 读取工作区 tenant 中的团队收件箱 id，不存在或不属于这个工作区时返回 ErrNotFound。lock 为 true 时锁住这一行。
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

// toDTOs 把团队收件箱转换为接口中的团队收件箱：一次查出各团队收件箱的人数。
func toDTOs(ctx context.Context, db bun.IDB, teams []Team) ([]TeamDTO, error) {
	out := make([]TeamDTO, len(teams))
	if len(teams) == 0 {
		return out, nil
	}
	ids := make([]string, len(teams))
	for i, t := range teams {
		ids[i] = t.ID
		out[i] = TeamDTO{
			ID: t.ID, Name: t.Name, Description: t.Description, Assignment: t.Assignment,
			Status: t.Status(), DisabledAt: t.DisabledAt, BusinessHours: t.BusinessHours,
		}
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

// OnMemberRemoved 是 member.Hooks 的 Removed：成员被移出工作区后，把他从这个工作区的团队收件箱中删除，别的工作区的团队收件箱不受影响。
func OnMemberRemoved(ctx context.Context, tx database.Tx, tenant, memberID string) error {
	_, err := tx.NewDelete().Model((*Membership)(nil)).
		Where("member_id = ? AND team_id IN (SELECT id FROM teams WHERE tenant_id = ?)", memberID, tenant).Exec(ctx)
	return errors.Wrap(err, "remove member from teams")
}
