// Package macro 是工作区的快捷回复：预先写好的回复模板（支持插入联系人与成员字段）和可选的附带会话操作。
// 快捷回复分工作区共享（owner_id 为空，需要 macros:manage 权限管理）和成员个人（owner_id 为该成员，仅本人可见与管理）。
package macro

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tag"
	"agents-desk/internal/team"
)

var (
	// ErrNotFound 是快捷回复不存在、属于别的工作区，或者属于其他成员的个人快捷回复。
	ErrNotFound = errorx.Define(errorx.NotFound, "macro.not_found")
	// ErrForbidden 是调用者没有权限管理该快捷回复（例如没有 macros:manage 权限去改共享快捷回复）。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "macro.forbidden")
	// ErrEmpty 是回复正文与附带操作同时为空。
	ErrEmpty = errorx.Define(errorx.InvalidArgument, "macro.empty")
	// ErrInvalidShortcut 是快捷指令码格式不合法（只能包含小写字母、数字、连字符和下划线，且以字母或数字开头）。
	ErrInvalidShortcut = errorx.Define(errorx.InvalidArgument, "macro.invalid_shortcut")
	// ErrShortcutTaken 是同一作用域下已存在相同的快捷指令码。
	ErrShortcutTaken = errorx.Define(errorx.Conflict, "macro.shortcut_taken")
	// ErrInvalidActions 是附带操作的参数不完整或互相冲突。
	ErrInvalidActions = errorx.Define(errorx.InvalidArgument, "macro.invalid_actions")
)

var shortcutPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Scope 是快捷回复的作用域：工作区共享或成员个人。
// 数据库通过 owner_id IS NULL / IS NOT NULL 单列表达，接口层映射为明确的枚举值。
type Scope string

const (
	ScopeShared   Scope = "shared"   // 工作区共享：全体成员可用，拥有 macros:manage 权限的成员可管理
	ScopePersonal Scope = "personal" // 成员个人：仅创建者本人可见、可用、可管理
)

// Scopes 是全部作用域，显示文字是翻译文件中 macro.scope.<作用域> 的消息。
var Scopes = enum.New("macro.scope", enum.Item(ScopeShared), enum.Item(ScopePersonal))

// Schema 让 OpenAPI 中的 Scope 只接受上面的取值，并带有显示文字。
func (Scope) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Scopes) }

// SnoozePreset 是快捷回复模板中稍后处理的相对时间预设；使用时由客户端按成员时区展开为具体时间点。
type SnoozePreset string

const (
	SnoozeLater    SnoozePreset = "later"    // 今天晚些时候（3 小时后）
	SnoozeTomorrow SnoozePreset = "tomorrow" // 明天 9 点
	SnoozeMonday   SnoozePreset = "monday"   // 下周一 9 点
	SnoozeWeek     SnoozePreset = "week"     // 一周后
	SnoozeMonth    SnoozePreset = "month"    // 一个月后
)

// SnoozePresets 是全部稍后处理相对预设，显示文字是翻译文件中 macro.snooze.<预设> 的消息。
var SnoozePresets = enum.New("macro.snooze",
	enum.Item(SnoozeLater), enum.Item(SnoozeTomorrow), enum.Item(SnoozeMonday),
	enum.Item(SnoozeWeek), enum.Item(SnoozeMonth))

// Schema 让 OpenAPI 中的 SnoozePreset 只接受上面的取值，并带有显示文字。
func (SnoozePreset) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, SnoozePresets)
}

// StatusAction 是快捷回复模板中的状态变更配置：关闭或按相对预设稍后处理。
type StatusAction struct {
	Kind   conversation.StatusActionKind `json:"kind" doc:"状态操作：close 关闭会话，snooze 稍后处理"`
	Snooze *SnoozePreset                 `json:"snooze,omitempty" doc:"kind 为 snooze 时必填：相对稍后处理预设"`
}

// Actions 是快捷回复模板附带的会话操作配置。
type Actions struct {
	Team          *conversation.TeamChange     `json:"team,omitempty" doc:"修改团队收件箱"`
	Assignee      *conversation.AssigneeChange `json:"assignee,omitempty" doc:"修改负责人"`
	Priority      *conversation.Priority       `json:"priority,omitempty" doc:"修改优先级"`
	AddTagIDs     []string                     `json:"add_tag_ids,omitempty" maxItems:"20" doc:"给会话添加的标签 ID 列表"`
	SetAttributes map[string]any               `json:"set_attributes,omitempty" doc:"修改会话自定义属性，值为 null 时清空"`
	Status        *StatusAction                `json:"status,omitempty" doc:"关闭或稍后处理会话"`
}

// HasChanges 报告模板附带操作是否至少配置了一项。
func (a Actions) HasChanges() bool {
	return a.Team != nil || a.Assignee != nil || a.Priority != nil ||
		len(a.AddTagIDs) > 0 || len(a.SetAttributes) > 0 || a.Status != nil
}

// Macro 是数据库中的一条快捷回复记录。
type Macro struct {
	bun.BaseModel `bun:"table:macros"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID  string  `bun:"tenant_id,notnull"`
	OwnerID   *string `bun:"owner_id"` // 为空表示共享，非空表示个人
	Shortcut  string  `bun:"shortcut,notnull"`
	Title     string  `bun:"title,notnull"`
	Body      string  `bun:"body,notnull"`
	Actions   Actions `bun:"actions,type:jsonb,notnull"`
	CreatedBy *string `bun:"created_by"`
}

// Scope 返回快捷回复 m 的作用域。
func (m *Macro) Scope() Scope {
	if m.OwnerID == nil {
		return ScopeShared
	}
	return ScopePersonal
}

// MacroDTO 是接口中的快捷回复。
type MacroDTO struct {
	ID        string    `json:"id"`
	Scope     Scope     `json:"scope" doc:"作用域：shared 共享，personal 个人"`
	Shortcut  string    `json:"shortcut" doc:"可选快捷指令码，在回复框中输入 /shortcut 快速触发"`
	Title     string    `json:"title"`
	Body      string    `json:"body" doc:"回复模板正文，可含 {{contact.name}} 等变量"`
	Actions   Actions   `json:"actions" doc:"附带执行的会话操作配置"`
	Editable  bool      `json:"editable" doc:"当前成员是否有权修改或删除这条快捷回复"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DTO 把快捷回复 m 转换为接口形式，按 sub 计算是否可编辑。
func (m *Macro) DTO(sub identity.Subject) MacroDTO {
	return MacroDTO{
		ID:        m.ID,
		Scope:     m.Scope(),
		Shortcut:  m.Shortcut,
		Title:     m.Title,
		Body:      m.Body,
		Actions:   m.Actions,
		Editable:  canManage(sub, m),
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// MacroOutput 是返回单条快捷回复的响应。
type MacroOutput struct {
	Body MacroDTO
}

// IDInput 是按路径参数 id 读取或删除快捷回复的请求。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// CreateParams 是新建快捷回复的参数。
type CreateParams struct {
	Scope    Scope   `json:"scope" doc:"作用域：shared 共享（需要 macros:manage 权限），personal 个人"`
	Title    string  `json:"title" minLength:"1" maxLength:"100" doc:"标题"`
	Shortcut string  `json:"shortcut,omitempty" maxLength:"32" doc:"可选快捷指令码（小写字母、数字、连字符和下划线）"`
	Body     string  `json:"body,omitempty" maxLength:"10000" doc:"回复模板正文，可含 {{...}} 变量占位符"`
	Actions  Actions `json:"actions,omitempty" doc:"附带执行的会话操作配置"`
}

// UpdateParams 是修改快捷回复的参数（作用域创建后不可修改）。
type UpdateParams struct {
	Title    string  `json:"title" minLength:"1" maxLength:"100" doc:"标题"`
	Shortcut string  `json:"shortcut,omitempty" maxLength:"32" doc:"可选快捷指令码（小写字母、数字、连字符和下划线）"`
	Body     string  `json:"body,omitempty" maxLength:"10000" doc:"回复模板正文，可含 {{...}} 变量占位符"`
	Actions  Actions `json:"actions,omitempty" doc:"附带执行的会话操作配置"`
}

// canManageShared 报告主体 sub 是否有权管理共享快捷回复。
func canManageShared(sub identity.Subject) bool {
	return sub.Type == identity.System || sub.Can(string(auth.MacrosManage))
}

// canManage 报告主体 sub 是否有权修改或删除快捷回复 m。
func canManage(sub identity.Subject, m *Macro) bool {
	if m.OwnerID == nil {
		return canManageShared(sub)
	}
	return sub.Type == member.Type && *m.OwnerID == sub.ID
}

// findVisible 读取 sub 在当前工作区中可见的快捷回复 id；lock 为 true 时加行锁。
// 跨工作区或属于他人的个人快捷回复均返回 ErrNotFound。
func findVisible(ctx context.Context, db bun.IDB, sub identity.Subject, id string, lock bool) (*Macro, error) {
	m := new(Macro)
	q := db.NewSelect().Model(m).Where("id = ? AND tenant_id = ?", id, sub.Tenant)
	if sub.Type == member.Type {
		q = q.Where("(owner_id IS NULL OR owner_id = ?)", sub.ID)
	} else {
		q = q.Where("owner_id IS NULL")
	}
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return m, errors.Wrap(err, "load macro")
}

// validateAndNormalize 规范化并校验标题、快捷指令码、正文及附带操作引用。
func validateAndNormalize(ctx context.Context, db bun.IDB, roles *authz.Store, tenantID string, title, shortcut, body string, actions Actions) (string, string, string, Actions, error) {
	title = strings.TrimSpace(title)
	shortcut = strings.ToLower(strings.TrimSpace(shortcut))
	if shortcut != "" && !shortcutPattern.MatchString(shortcut) {
		return "", "", "", Actions{}, ErrInvalidShortcut.New(errorx.Field("shortcut"))
	}
	body = strings.TrimSpace(body)

	norm, err := validateActions(ctx, db, roles, tenantID, actions)
	if err != nil {
		return "", "", "", Actions{}, err
	}
	if body == "" && !norm.HasChanges() {
		return "", "", "", Actions{}, ErrEmpty.New(errorx.Field("body"))
	}
	return title, shortcut, body, norm, nil
}

// validateActions 校验并规范化模板中的附带会话操作：验证引用的团队、成员、标签和会话自定义属性均属于当前工作区且有效。
func validateActions(ctx context.Context, db bun.IDB, roles *authz.Store, tenantID string, in Actions) (Actions, error) {
	var out Actions

	if in.Team != nil {
		switch in.Team.Mode {
		case conversation.TeamSet:
			if _, err := uuid.Parse(in.Team.ID); err != nil {
				return Actions{}, ErrInvalidActions.New(errorx.Field("actions.team.id"))
			}
			var row struct {
				DisabledAt *time.Time `bun:"disabled_at"`
			}
			err := db.NewSelect().Table("teams").Column("disabled_at").Where("id = ? AND tenant_id = ?", in.Team.ID, tenantID).Scan(ctx, &row)
			if errors.Is(err, sql.ErrNoRows) {
				return Actions{}, team.ErrNotFound.New(errorx.Field("actions.team.id"))
			}
			if err != nil {
				return Actions{}, errors.Wrap(err, "check macro team")
			}
			if row.DisabledAt != nil {
				return Actions{}, team.ErrDisabled.New(errorx.Field("actions.team.id"))
			}
			out.Team = &conversation.TeamChange{Mode: conversation.TeamSet, ID: in.Team.ID}
		case conversation.TeamClear:
			out.Team = &conversation.TeamChange{Mode: conversation.TeamClear}
		default:
			return Actions{}, ErrInvalidActions.New(errorx.Field("actions.team.mode"))
		}
	}

	if in.Assignee != nil {
		switch in.Assignee.Mode {
		case conversation.AssigneeMe:
			out.Assignee = &conversation.AssigneeChange{Mode: conversation.AssigneeMe}
		case conversation.AssigneeClear:
			out.Assignee = &conversation.AssigneeChange{Mode: conversation.AssigneeClear}
		case conversation.AssigneeMember:
			if _, err := uuid.Parse(in.Assignee.ID); err != nil {
				return Actions{}, ErrInvalidActions.New(errorx.Field("actions.assignee.id"))
			}
			ref := identity.Ref{Type: member.Type, ID: in.Assignee.ID}
			if _, err := member.RoleOf(ctx, roles.WithDB(db), ref, tenantID); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return Actions{}, member.ErrNotFound.New(errorx.Field("actions.assignee.id"))
				}
				return Actions{}, err
			}
			out.Assignee = &conversation.AssigneeChange{Mode: conversation.AssigneeMember, ID: in.Assignee.ID}
		default:
			return Actions{}, ErrInvalidActions.New(errorx.Field("actions.assignee.mode"))
		}
	}

	if in.Priority != nil {
		if !slices.Contains(conversation.Priorities.Values(), *in.Priority) {
			return Actions{}, ErrInvalidActions.New(errorx.Field("actions.priority"))
		}
		p := *in.Priority
		out.Priority = &p
	}

	if len(in.AddTagIDs) > 0 {
		seen := make(map[string]struct{}, len(in.AddTagIDs))
		tagIDs := make([]string, 0, len(in.AddTagIDs))
		for _, id := range in.AddTagIDs {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			if _, err := uuid.Parse(id); err != nil {
				return Actions{}, ErrInvalidActions.New(errorx.Field("actions.add_tag_ids"))
			}
			if _, err := tag.Find(ctx, db, tenantID, id, false); err != nil {
				if errors.Is(err, tag.ErrNotFound) {
					return Actions{}, tag.ErrNotFound.New(errorx.Field("actions.add_tag_ids"))
				}
				return Actions{}, err
			}
			tagIDs = append(tagIDs, id)
		}
		out.AddTagIDs = tagIDs
	}

	if len(in.SetAttributes) > 0 {
		// 用当前会话属性定义校验每个键和值是否合法，同时保留显式传入的 null（用于清空该属性）
		norm, _, err := attribute.ApplyPatch(ctx, db, tenantID, attribute.TargetConversation, nil, in.SetAttributes)
		if err != nil {
			return Actions{}, err
		}
		attrs := make(map[string]any, len(in.SetAttributes))
		for k, raw := range in.SetAttributes {
			if v, ok := norm[k]; ok {
				attrs[k] = v
			} else if raw == nil {
				attrs[k] = nil
			}
		}
		if len(attrs) > 0 {
			out.SetAttributes = attrs
		}
	}

	if in.Status != nil {
		switch in.Status.Kind {
		case conversation.StatusActionClose:
			out.Status = &StatusAction{Kind: conversation.StatusActionClose}
		case conversation.StatusActionSnooze:
			if in.Status.Snooze == nil || !slices.Contains(SnoozePresets.Values(), *in.Status.Snooze) {
				return Actions{}, ErrInvalidActions.New(errorx.Field("actions.status.snooze"))
			}
			s := *in.Status.Snooze
			out.Status = &StatusAction{Kind: conversation.StatusActionSnooze, Snooze: &s}
		default:
			return Actions{}, ErrInvalidActions.New(errorx.Field("actions.status.kind"))
		}
	}

	return out, nil
}

// OnMemberRemoved 在成员被移出工作区时删除其在该工作区中的所有个人快捷回复，共享快捷回复保留。
// 由 member.Hooks.Removed 在移出成员的同一事务中调用。
func OnMemberRemoved(ctx context.Context, tx database.Tx, tenantID, memberID string) error {
	_, err := tx.NewDelete().Model((*Macro)(nil)).Where("tenant_id = ? AND owner_id = ?", tenantID, memberID).Exec(ctx)
	return errors.Wrap(err, "delete removed member macros")
}

// isShortcutConflict 判断数据库错误是否为快捷指令码唯一约束冲突。
func isShortcutConflict(err error) bool {
	return database.IsUniqueViolation(err, "uq_macros_tenant_shared_shortcut") ||
		database.IsUniqueViolation(err, "uq_macros_tenant_personal_shortcut")
}
