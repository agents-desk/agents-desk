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

// UpdateParams 是修改工单类型的请求。
type UpdateParams struct {
	Name              string     `json:"name" minLength:"1" maxLength:"60"`
	Description       string     `json:"description,omitempty" maxLength:"200"`
	DefaultTeamID     *string    `json:"default_team_id,omitempty" format:"uuid" doc:"默认进入的团队收件箱 ID，不填或为空表示清除"`
	DefaultVisibility Visibility `json:"default_visibility" doc:"新建工单的默认可见范围"`
	Position          *int       `json:"position,omitempty" minimum:"0" doc:"展示顺序，不填保持不变"`
}

// UpdateInput 是修改工单类型的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateParams
}

// Update 修改当前工作区中的一个工单类型。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改工单类型的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 修改 sub 所在工作区中的工单类型 id 并记录操作日志。
// 不存在返回 ErrNotFound；重名返回 ErrNameTaken；更换默认团队为不存在或已停用团队时返回对应业务错误。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, p UpdateParams) (*TicketTypeDTO, error) {
	name := strings.TrimSpace(p.Name)
	desc := strings.TrimSpace(p.Description)
	teamID := cleanTeamID(p.DefaultTeamID)
	var updated *TicketType
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := Find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		allowDisabled := samePtr(t.DefaultTeamID, teamID)
		if err := checkDefaultTeam(ctx, tx, sub.Tenant, teamID, allowDisabled); err != nil {
			return err
		}
		pos := t.Position
		if p.Position != nil {
			pos = *p.Position
		}
		var changed []string
		if t.Name != name {
			changed = append(changed, "name")
		}
		if t.Description != desc {
			changed = append(changed, "description")
		}
		if !samePtr(t.DefaultTeamID, teamID) {
			changed = append(changed, "default_team_id")
		}
		if t.DefaultVisibility != p.DefaultVisibility {
			changed = append(changed, "default_visibility")
		}
		if t.Position != pos {
			changed = append(changed, "position")
		}
		updated = t
		if len(changed) == 0 {
			return nil
		}
		t.Name, t.Description, t.DefaultTeamID, t.DefaultVisibility, t.Position = name, desc, teamID, p.DefaultVisibility, pos
		if _, err := tx.NewUpdate().Model(t).
			Column("name", "description", "default_team_id", "default_visibility", "position", "updated_at").
			WherePK().Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "ticket_types_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", name))
			}
			return errors.Wrap(err, "update ticket type")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "ticket_type.updated", TargetType: "ticket_type", TargetID: id,
			Properties: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.db, updated)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 PUT /t/{slug}/api/ticket-types/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*TicketTypeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &TicketTypeOutput{Body: *dto}, nil
}
