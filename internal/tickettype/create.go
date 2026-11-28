package tickettype

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// CreateParams 是新建工单类型的请求。
type CreateParams struct {
	Name              string     `json:"name" minLength:"1" maxLength:"60"`
	Description       string     `json:"description,omitempty" maxLength:"200"`
	DefaultTeamID     *string    `json:"default_team_id,omitempty" format:"uuid" doc:"默认进入的团队收件箱 ID，留空表示不指定"`
	DefaultVisibility Visibility `json:"default_visibility,omitempty" doc:"新建工单的默认可见范围，不填默认为 all（客户可见）"`
	Position          *int       `json:"position,omitempty" minimum:"0" doc:"展示顺序，不填排在最后"`
}

// CreateInput 是新建工单类型的 HTTP 请求。
type CreateInput struct {
	Body CreateParams
}

// Create 在当前工作区中新建工单类型。
type Create struct {
	db *bun.DB
}

// NewCreate 返回新建工单类型的动作。
func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 在 sub 所在工作区中新建工单类型并记录操作日志。
// 同工作区重名（忽略大小写）返回 ErrNameTaken；默认团队不存在返回 team.ErrNotFound，已停用返回 team.ErrDisabled。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, p CreateParams) (*TicketTypeDTO, error) {
	name := strings.TrimSpace(p.Name)
	desc := strings.TrimSpace(p.Description)
	vis := p.DefaultVisibility
	if vis == "" {
		vis = VisibilityAll
	}
	teamID := cleanTeamID(p.DefaultTeamID)
	t := &TicketType{
		ID:                database.NewID(),
		TenantID:          sub.Tenant,
		Name:              name,
		Description:       desc,
		DefaultTeamID:     teamID,
		DefaultVisibility: vis,
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := checkDefaultTeam(ctx, tx, sub.Tenant, teamID, false); err != nil {
			return err
		}
		if p.Position != nil {
			t.Position = *p.Position
		} else {
			var next int
			if err := tx.NewRaw(`SELECT COALESCE(MAX(position) + 1, 0) FROM ticket_types WHERE tenant_id = ?`, sub.Tenant).Scan(ctx, &next); err != nil {
				return errors.Wrap(err, "next ticket type position")
			}
			t.Position = next
		}
		if _, err := tx.NewInsert().Model(t).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "ticket_types_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", name))
			}
			return errors.Wrap(err, "insert ticket type")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "ticket_type.created", TargetType: "ticket_type", TargetID: t.ID,
			Properties: map[string]any{"name": t.Name, "default_visibility": string(t.DefaultVisibility)},
		})
	})
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.db, t)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/ticket-types。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*TicketTypeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TicketTypeOutput{Body: *dto}, nil
}
