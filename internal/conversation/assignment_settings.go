package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
)

var (
	// ErrDefaultTeamRequired 是选择默认指派到团队收件箱但未指定团队 ID。
	ErrDefaultTeamRequired = errorx.Define(errorx.InvalidArgument, "conversation.default_team_required")
	// ErrDefaultMemberRequired 是选择默认指派给成员但未指定成员 ID。
	ErrDefaultMemberRequired = errorx.Define(errorx.InvalidArgument, "conversation.default_member_required")
)

// DefaultTarget 是工作区默认指派的目标类型。
type DefaultTarget string

const (
	DefaultNone   DefaultTarget = "none"   // 不自动指派，新会话保持未指派
	DefaultTeam   DefaultTarget = "team"   // 进入指定的默认团队收件箱
	DefaultMember DefaultTarget = "member" // 直接指派给指定的默认负责人
)

// DefaultTargets 是全部默认指派目标，显示文字是翻译文件中 conversation.default_target.<目标> 的消息。
var DefaultTargets = enum.New("conversation.default_target", enum.Item(DefaultNone), enum.Item(DefaultTeam), enum.Item(DefaultMember))

// Schema 让 OpenAPI 中的 DefaultTarget 只接受上面的取值，并带有显示文字。
func (DefaultTarget) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, DefaultTargets)
}

// AssignmentSettings 是工作区级的默认指派、自动关闭与超时改派设置，当渠道没有单独指定默认团队收件箱时生效。
// 字段不带 omitempty，保证清空目标时能完整覆盖旧值。
type AssignmentSettings struct {
	DefaultTarget       DefaultTarget `json:"default_target" doc:"默认指派目标：none 不自动指派，team 默认团队收件箱，member 默认成员"`
	DefaultTeamID       string        `json:"default_team_id" doc:"默认团队收件箱 ID（default_target 为 team 时有效）"`
	DefaultMemberID     string        `json:"default_member_id" doc:"默认成员 ID（default_target 为 member 时有效）"`
	AutoCloseAfterHours int           `json:"auto_close_after_hours" doc:"客户停止回复后自动关闭服务型会话的小时数，0 表示关闭"`
	SLAReassignMinutes  int           `json:"sla_reassign_minutes" doc:"会话等待回复超时后自动改派同团队在线成员的分钟数，0 表示关闭"`
}

// AssignmentSettingsOutput 是读取或保存指派设置的 HTTP 响应。
type AssignmentSettingsOutput struct {
	Body AssignmentSettings
}

// AssignmentSettingsInput 是修改指派设置的 HTTP 请求。
type AssignmentSettingsInput struct {
	Body struct {
		DefaultTarget       DefaultTarget `json:"default_target" doc:"默认指派目标：none、team 或 member"`
		DefaultTeamID       string        `json:"default_team_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"默认团队收件箱 ID"`
		DefaultMemberID     string        `json:"default_member_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"默认成员 ID"`
		AutoCloseAfterHours int           `json:"auto_close_after_hours,omitempty" minimum:"0" maximum:"720" doc:"客户停止回复后自动关闭会话的小时数，0 表示关闭"`
		SLAReassignMinutes  int           `json:"sla_reassign_minutes,omitempty" minimum:"0" maximum:"10080" doc:"会话等待回复超时后自动改派的分钟数，0 表示关闭"`
	}
}

// NewAssignmentSettingsGroup 返回工作区默认指派设置组，保存在 settings 表中，键名为 assignment。
func NewAssignmentSettingsGroup(db bun.IDB) *settings.Group[AssignmentSettings] {
	return settings.New(db, "assignment", AssignmentSettings{DefaultTarget: DefaultNone})
}

// AssignmentSettingsActions 是工作区指派设置的读取与保存动作。
type AssignmentSettingsActions struct {
	db *bun.DB
}

// NewAssignmentSettingsActions 返回工作区指派设置的读取与保存动作。
func NewAssignmentSettingsActions(db *bun.DB) *AssignmentSettingsActions {
	return &AssignmentSettingsActions{db: db}
}

// Get 返回 sub 所在工作区的默认指派设置。
func (a *AssignmentSettingsActions) Get(ctx context.Context, sub identity.Subject) (*AssignmentSettings, error) {
	s, err := NewAssignmentSettingsGroup(a.db).Tenant(sub.Tenant).Get(ctx)
	if err != nil {
		return nil, err
	}
	if s.DefaultTarget == "" {
		s.DefaultTarget = DefaultNone
	}
	return &s, nil
}

// Save 保存 sub 所在工作区的默认指派、自动关闭与超时改派设置，校验目标团队未停用或目标成员属于当前工作区，并记录操作日志。
func (a *AssignmentSettingsActions) Save(ctx context.Context, sub identity.Subject, p AssignmentSettings) (*AssignmentSettings, error) {
	clean := AssignmentSettings{
		DefaultTarget:       p.DefaultTarget,
		AutoCloseAfterHours: max(0, min(p.AutoCloseAfterHours, 720)),
		SLAReassignMinutes:  max(0, min(p.SLAReassignMinutes, 10080)),
	}
	switch p.DefaultTarget {
	case DefaultNone:
	case DefaultTeam:
		if p.DefaultTeamID == "" {
			return nil, ErrDefaultTeamRequired.New(errorx.Field("default_team_id"))
		}
		clean.DefaultTeamID = p.DefaultTeamID
	case DefaultMember:
		if p.DefaultMemberID == "" {
			return nil, ErrDefaultMemberRequired.New(errorx.Field("default_member_id"))
		}
		clean.DefaultMemberID = p.DefaultMemberID
	default:
		clean.DefaultTarget = DefaultNone
	}

	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		switch clean.DefaultTarget {
		case DefaultTeam:
			var row struct {
				DisabledAt *time.Time `bun:"disabled_at"`
			}
			err := tx.NewSelect().Table("teams").Column("disabled_at").
				Where("id = ? AND tenant_id = ?", clean.DefaultTeamID, sub.Tenant).Scan(ctx, &row)
			if errors.Is(err, sql.ErrNoRows) {
				return team.ErrNotFound.New(errorx.Field("default_team_id"))
			}
			if err != nil {
				return errors.Wrap(err, "check default assignment team")
			}
			if row.DisabledAt != nil {
				return team.ErrDisabled.New(errorx.Field("default_team_id"))
			}
		case DefaultMember:
			ref := identity.Ref{Type: member.Type, ID: clean.DefaultMemberID}
			if _, err := member.RoleOf(ctx, authz.NewStore(tx, auth.Catalog), ref, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return member.ErrNotFound.New(errorx.Field("default_member_id"))
				}
				return err
			}
		}
		if err := NewAssignmentSettingsGroup(tx).Tenant(sub.Tenant).Update(ctx, func(s *AssignmentSettings) error {
			*s = clean
			return nil
		}); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "assignment_settings.updated", TargetType: "tenant", TargetID: sub.Tenant,
			Properties: map[string]any{
				"default_target":         clean.DefaultTarget,
				"default_team_id":        clean.DefaultTeamID,
				"default_member_id":      clean.DefaultMemberID,
				"auto_close_after_hours": clean.AutoCloseAfterHours,
				"sla_reassign_minutes":   clean.SLAReassignMinutes,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return &clean, nil
}

// GetHTTP 处理 GET /t/{slug}/api/settings/assignment。
func (a *AssignmentSettingsActions) GetHTTP(ctx context.Context, _ *struct{}) (*AssignmentSettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	s, err := a.Get(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &AssignmentSettingsOutput{Body: *s}, nil
}

// UpdateHTTP 处理 PUT /t/{slug}/api/settings/assignment。
func (a *AssignmentSettingsActions) UpdateHTTP(ctx context.Context, in *AssignmentSettingsInput) (*AssignmentSettingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	s, err := a.Save(ctx, sub, AssignmentSettings{
		DefaultTarget:       in.Body.DefaultTarget,
		DefaultTeamID:       in.Body.DefaultTeamID,
		DefaultMemberID:     in.Body.DefaultMemberID,
		AutoCloseAfterHours: in.Body.AutoCloseAfterHours,
		SLAReassignMinutes:  in.Body.SLAReassignMinutes,
	})
	if err != nil {
		return nil, err
	}
	return &AssignmentSettingsOutput{Body: *s}, nil
}
