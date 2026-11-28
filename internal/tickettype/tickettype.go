// Package tickettype 是工作区的工单类型管理：定义工单模板、默认团队收件箱、默认可见范围、展示顺序与停用状态，
// 每种类型可以配置独立的自定义字段（见 internal/attribute 中 target=ticket 的属性定义）。
package tickettype

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/team"
)

var (
	// ErrNotFound 是工单类型不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "ticket_type.not_found")
	// ErrNameTaken 是同一个工作区中已有同名的工单类型（忽略大小写）。
	ErrNameTaken = errorx.Define(errorx.Conflict, "ticket_type.name_taken")
	// ErrDisabled 是工单类型已停用，不能用于新建工单。
	ErrDisabled = errorx.Define(errorx.Conflict, "ticket_type.disabled")
	// ErrInUse 是已有工单引用该类型，不能删除，可以改为停用。
	ErrInUse = errorx.Define(errorx.Conflict, "ticket_type.in_use")
)

// Visibility 是工单默认的可见范围，与会话时间线的 visibility 一致。
type Visibility string

const (
	VisibilityAll     Visibility = "all"     // 客户可见：关联会话的客户可以看到工单进度与公开更新
	VisibilityMembers Visibility = "members" // 仅成员可见：用于团队内部协作
)

// Visibilities 是全部工单可见范围，显示文字是翻译文件中 ticket_type.visibility.<范围> 的消息。
var Visibilities = enum.New("ticket_type.visibility", enum.Item(VisibilityAll), enum.Item(VisibilityMembers))

// Schema 让 OpenAPI 中的 Visibility 只接受上面的取值，并带有显示文字。
func (Visibility) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Visibilities) }

// Status 是工单类型的启用状态。
type Status string

const (
	Active   Status = "active"   // 正常启用
	Disabled Status = "disabled" // 已停用：不能用于新建工单，历史工单保留原类型与字段
)

// Statuses 是全部工单类型状态，显示文字是翻译文件中 ticket_type.status.<状态> 的消息。
var Statuses = enum.New("ticket_type.status", enum.Item(Active), enum.Item(Disabled))

// Schema 让 OpenAPI 中的 Status 只接受上面的取值，并带有显示文字。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

// TicketType 是工作区中的一个工单类型。
type TicketType struct {
	bun.BaseModel `bun:"table:ticket_types"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID          string     `bun:"tenant_id,notnull"`
	Name              string     `bun:"name,notnull"`
	Description       string     `bun:"description,notnull"`
	DefaultTeamID     *string    `bun:"default_team_id"`
	DefaultVisibility Visibility `bun:"default_visibility,notnull"`
	Position          int        `bun:"position,notnull"`
	DisabledAt        *time.Time `bun:"disabled_at"`
}

// Status 返回工单类型当前的启用状态。
func (t *TicketType) Status() Status {
	if t.DisabledAt != nil {
		return Disabled
	}
	return Active
}

// TicketTypeDTO 是接口中的工单类型，附带默认团队收件箱名称和自定义字段数量。
type TicketTypeDTO struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	DefaultTeamID     *string    `json:"default_team_id,omitempty" doc:"默认团队收件箱 ID，为空表示不指定默认团队"`
	DefaultTeamName   string     `json:"default_team_name,omitempty" doc:"默认团队收件箱名称"`
	DefaultVisibility Visibility `json:"default_visibility" doc:"新建工单的默认可见范围"`
	Position          int        `json:"position"`
	Status            Status     `json:"status" doc:"状态：正常或已停用"`
	DisabledAt        *time.Time `json:"disabled_at,omitempty" doc:"停用时间，正常启用时为空"`
	FieldCount        int        `json:"field_count" doc:"该工单类型下的自定义字段数量"`
}

// TicketTypeOutput 是返回一个工单类型的响应。
type TicketTypeOutput struct {
	Body TicketTypeDTO
}

// IDInput 是按路径参数 id 读取、停用、启用或删除工单类型的请求。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Find 读取工作区 tenant 中的工单类型 id；lock 为 true 时加行锁。不存在或属于别的工作区时返回 ErrNotFound。
func Find(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*TicketType, error) {
	t := new(TicketType)
	q := db.NewSelect().Model(t).Where("id = ? AND tenant_id = ?", id, tenant)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return t, errors.Wrap(err, "load ticket type")
}

// FindActive 读取工作区 tenant 中未停用的工单类型 id；不存在时返回 ErrNotFound，已停用时返回 ErrDisabled。
func FindActive(ctx context.Context, db bun.IDB, tenant, id string) (*TicketType, error) {
	t, err := Find(ctx, db, tenant, id, false)
	if err != nil {
		return nil, err
	}
	if t.DisabledAt != nil {
		return nil, ErrDisabled.New()
	}
	return t, nil
}

// checkDefaultTeam 校验默认团队收件箱属于当前工作区且未停用；为空时直接放行。
// allowDisabled 为 true 时放行保持原样的已停用团队（更新工单类型未改动默认团队时使用）。
func checkDefaultTeam(ctx context.Context, db bun.IDB, tenant string, teamID *string, allowDisabled bool) error {
	if teamID == nil || *teamID == "" {
		return nil
	}
	var row struct {
		DisabledAt *time.Time `bun:"disabled_at"`
	}
	err := db.NewSelect().Table("teams").Column("disabled_at").
		Where("id = ? AND tenant_id = ?", *teamID, tenant).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return team.ErrNotFound.New(errorx.Field("default_team_id"))
	}
	if err != nil {
		return errors.Wrap(err, "check default team")
	}
	if !allowDisabled && row.DisabledAt != nil {
		return team.ErrDisabled.New(errorx.Field("default_team_id"))
	}
	return nil
}

// toDTO 把单个工单类型转为 DTO，同时查出默认团队名称与字段数量。
func toDTO(ctx context.Context, db bun.IDB, t *TicketType) (TicketTypeDTO, error) {
	list, err := toDTOs(ctx, db, t.TenantID, []TicketType{*t})
	if err != nil {
		return TicketTypeDTO{}, err
	}
	return list[0], nil
}

// toDTOs 批量把工单类型转为 DTO，一次查询补齐默认团队名称与自定义字段数量。
func toDTOs(ctx context.Context, db bun.IDB, tenant string, rows []TicketType) ([]TicketTypeDTO, error) {
	out := make([]TicketTypeDTO, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]string, len(rows))
	teamIDs := make([]string, 0, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
		if rows[i].DefaultTeamID != nil && *rows[i].DefaultTeamID != "" {
			teamIDs = append(teamIDs, *rows[i].DefaultTeamID)
		}
	}

	teamNames := map[string]string{}
	if len(teamIDs) > 0 {
		var teams []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("teams").Column("id", "name").
			Where("tenant_id = ? AND id IN (?)", tenant, bun.In(teamIDs)).Scan(ctx, &teams); err != nil {
			return nil, errors.Wrap(err, "load default team names")
		}
		for _, tm := range teams {
			teamNames[tm.ID] = tm.Name
		}
	}

	fieldCounts := map[string]int{}
	var counts []struct {
		TicketTypeID string `bun:"ticket_type_id"`
		Count        int    `bun:"cnt"`
	}
	if err := db.NewSelect().Table("attribute_definitions").
		ColumnExpr("ticket_type_id, count(*) AS cnt").
		Where("tenant_id = ? AND target = 'ticket' AND ticket_type_id IN (?)", tenant, bun.In(ids)).
		GroupExpr("ticket_type_id").Scan(ctx, &counts); err != nil {
		return nil, errors.Wrap(err, "count ticket type fields")
	}
	for _, c := range counts {
		fieldCounts[c.TicketTypeID] = c.Count
	}

	for i := range rows {
		r := rows[i]
		teamName := ""
		if r.DefaultTeamID != nil {
			teamName = teamNames[*r.DefaultTeamID]
		}
		out[i] = TicketTypeDTO{
			ID:                r.ID,
			Name:              r.Name,
			Description:       r.Description,
			DefaultTeamID:     r.DefaultTeamID,
			DefaultTeamName:   teamName,
			DefaultVisibility: r.DefaultVisibility,
			Position:          r.Position,
			Status:            r.Status(),
			DisabledAt:        r.DisabledAt,
			FieldCount:        fieldCounts[r.ID],
		}
	}
	return out, nil
}

// cleanTeamID 规范化可选团队 ID：空串视为 nil。
func cleanTeamID(id *string) *string {
	if id == nil || *id == "" {
		return nil
	}
	v := *id
	return &v
}

// samePtr 比较两个可选字符串指针是否相同。
func samePtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
