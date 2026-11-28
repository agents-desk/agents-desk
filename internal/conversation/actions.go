package conversation

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// ErrInvalidActions 是附带操作的参数不完整或互相冲突（例如空的批量操作、set 模式缺少 ID、snooze 缺少 until）。
var ErrInvalidActions = errorx.Define(errorx.InvalidArgument, "conversation.invalid_actions")

// TeamChangeMode 是修改团队收件箱的模式：指派到指定团队，或移出团队。
type TeamChangeMode string

const (
	TeamSet   TeamChangeMode = "set"   // 转到指定团队收件箱
	TeamClear TeamChangeMode = "clear" // 移出团队收件箱
)

// TeamChangeModes 是全部团队变更模式。
var TeamChangeModes = enum.New("conversation.team_change_mode", enum.Item(TeamSet), enum.Item(TeamClear))

// Schema 让 OpenAPI 中的 TeamChangeMode 只接受目录中的取值。
func (TeamChangeMode) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, TeamChangeModes)
}

// TeamChange 描述对团队收件箱的显式变更。
type TeamChange struct {
	Mode TeamChangeMode `json:"mode" doc:"变更方式：set 转到指定团队收件箱，clear 移出团队收件箱"`
	ID   string         `json:"id,omitempty" format:"uuid" doc:"mode 为 set 时的目标团队收件箱 ID"`
}

// AssigneeChangeMode 是修改负责人的模式：指派给当前成员、指定成员、指定机器人，或取消负责人。
type AssigneeChangeMode string

const (
	AssigneeMe     AssigneeChangeMode = "me"     // 指派给当前操作成员
	AssigneeMember AssigneeChangeMode = "member" // 指派给指定成员
	AssigneeBot    AssigneeChangeMode = "bot"    // 指派给指定机器人
	AssigneeClear  AssigneeChangeMode = "clear"  // 取消负责人
)

// AssigneeChangeModes 是全部负责人变更模式。
var AssigneeChangeModes = enum.New("conversation.assignee_change_mode", enum.Item(AssigneeMe), enum.Item(AssigneeMember), enum.Item(AssigneeBot), enum.Item(AssigneeClear))

// Schema 让 OpenAPI 中的 AssigneeChangeMode 只接受目录中的取值。
func (AssigneeChangeMode) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, AssigneeChangeModes)
}

// AssigneeChange 描述对负责人的显式变更。
type AssigneeChange struct {
	Mode AssigneeChangeMode `json:"mode" doc:"变更方式：me 指派给当前成员，member 指定成员，bot 指定机器人，clear 取消负责人"`
	ID   string             `json:"id,omitempty" format:"uuid" doc:"mode 为 member 或 bot 时的目标负责人 ID"`
}

// StatusActionKind 是会话状态变更的类型：关闭或稍后处理（二选一）。
type StatusActionKind string

const (
	StatusActionClose  StatusActionKind = "close"  // 关闭会话
	StatusActionSnooze StatusActionKind = "snooze" // 稍后处理
)

// StatusActionKinds 是全部状态变更类型。
var StatusActionKinds = enum.New("conversation.status_action_kind", enum.Item(StatusActionClose), enum.Item(StatusActionSnooze))

// Schema 让 OpenAPI 中的 StatusActionKind 只接受目录中的取值。
func (StatusActionKind) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, StatusActionKinds)
}

// StatusAction 描述状态变更：关闭或稍后处理到指定时间。
type StatusAction struct {
	Kind  StatusActionKind `json:"kind" doc:"状态操作：close 关闭会话，snooze 稍后处理"`
	Until *time.Time       `json:"until,omitempty" doc:"kind 为 snooze 时必填：回到打开的时间，必须晚于现在"`
}

// Actions 是可在单事务中原子执行的一组会话操作，用于快捷回复附带操作以及“发送并关闭/稍后处理”。
type Actions struct {
	Team          *TeamChange     `json:"team,omitempty" doc:"修改团队收件箱"`
	Assignee      *AssigneeChange `json:"assignee,omitempty" doc:"修改负责人"`
	Priority      *Priority       `json:"priority,omitempty" doc:"修改优先级"`
	AddTagIDs     []string        `json:"add_tag_ids,omitempty" maxItems:"20" doc:"给会话添加的标签 ID 列表"`
	SetAttributes map[string]any  `json:"set_attributes,omitempty" doc:"修改会话自定义属性，值为 null 时清空"`
	Status        *StatusAction   `json:"status,omitempty" doc:"关闭或稍后处理会话"`
}

// HasChanges 报告 Actions 是否包含至少一项需要执行的操作。
func (a Actions) HasChanges() bool {
	return a.Team != nil || a.Assignee != nil || a.Priority != nil ||
		len(a.AddTagIDs) > 0 || len(a.SetAttributes) > 0 || a.Status != nil
}

// HasAssignment 报告 Actions 是否包含显式的团队或负责人指派操作。
func (a Actions) HasAssignment() bool {
	return a.Team != nil || a.Assignee != nil
}

// toAssignment 把强类型的 TeamChange 与 AssigneeChange 转换为 Assignment，并校验必填 ID。
func (a Actions) toAssignment(sub identity.Subject) (Assignment, error) {
	var out Assignment
	if a.Team != nil {
		switch a.Team.Mode {
		case TeamSet:
			if _, err := uuid.Parse(a.Team.ID); err != nil {
				return Assignment{}, ErrInvalidActions.New(errorx.Field("actions.team.id"))
			}
			id := a.Team.ID
			out.TeamID = &id
		case TeamClear:
			empty := ""
			out.TeamID = &empty
		default:
			return Assignment{}, ErrInvalidActions.New(errorx.Field("actions.team.mode"))
		}
	}
	if a.Assignee != nil {
		switch a.Assignee.Mode {
		case AssigneeMe:
			if sub.Type == identity.System {
				return Assignment{}, ErrInvalidActions.New(errorx.Field("actions.assignee.mode"))
			}
			id := sub.ID
			out.AssigneeID = &id
		case AssigneeMember:
			if _, err := uuid.Parse(a.Assignee.ID); err != nil {
				return Assignment{}, ErrInvalidActions.New(errorx.Field("actions.assignee.id"))
			}
			typ := MemberActor
			id := a.Assignee.ID
			out.AssigneeType = &typ
			out.AssigneeID = &id
		case AssigneeBot:
			if _, err := uuid.Parse(a.Assignee.ID); err != nil {
				return Assignment{}, ErrInvalidActions.New(errorx.Field("actions.assignee.id"))
			}
			typ := BotActor
			id := a.Assignee.ID
			out.AssigneeType = &typ
			out.AssigneeID = &id
		case AssigneeClear:
			empty := ""
			out.AssigneeID = &empty
		default:
			return Assignment{}, ErrInvalidActions.New(errorx.Field("actions.assignee.mode"))
		}
	}
	return out, nil
}

// applyActions 在事务 tx 中按确定顺序对已锁住的会话 c 执行一组操作，返回产生的全部系统事件。
// 执行顺序：优先级 → 标签 → 自定义属性 → 指派 → 状态变更（关闭/稍后处理）。任一步失败时整笔事务回滚。
func applyActions(ctx context.Context, tx database.Tx, down Downstream, sub identity.Subject, c *Conversation, act Actions) ([]*Item, error) {
	if c.Kind == Chat {
		return nil, ErrWrongKind.New()
	}
	var items []*Item

	if act.Priority != nil {
		items = append(items, applyPriority(sub, c, *act.Priority)...)
	}

	if len(act.AddTagIDs) > 0 {
		seen := make(map[string]struct{}, len(act.AddTagIDs))
		for _, tagID := range act.AddTagIDs {
			if _, dup := seen[tagID]; dup {
				continue
			}
			seen[tagID] = struct{}{}
			if _, err := uuid.Parse(tagID); err != nil {
				return nil, ErrInvalidActions.New(errorx.Field("actions.add_tag_ids"))
			}
			ev, err := applyAddTag(ctx, tx, sub, c, tagID)
			if err != nil {
				return nil, err
			}
			items = append(items, ev...)
		}
	}

	if len(act.SetAttributes) > 0 {
		ev, err := applyAttributes(ctx, tx, sub, c, act.SetAttributes)
		if err != nil {
			return nil, err
		}
		items = append(items, ev...)
	}

	if act.HasAssignment() {
		p, err := act.toAssignment(sub)
		if err != nil {
			return nil, err
		}
		ev, err := applyAssign(ctx, tx, down, sub, c, p)
		if err != nil {
			return nil, err
		}
		items = append(items, ev...)
	}

	if act.Status != nil {
		switch act.Status.Kind {
		case StatusActionClose:
			ev, err := applyClose(ctx, tx, down, sub, c)
			if err != nil {
				return nil, err
			}
			items = append(items, ev...)
		case StatusActionSnooze:
			if act.Status.Until == nil {
				return nil, ErrInvalidActions.New(errorx.Field("actions.status.until"))
			}
			ev, err := applySnooze(sub, c, *act.Status.Until)
			if err != nil {
				return nil, err
			}
			items = append(items, ev...)
		default:
			return nil, ErrInvalidActions.New(errorx.Field("actions.status.kind"))
		}
	}

	return items, nil
}

// ApplyActionsInput 是批量执行会话操作的 HTTP 请求。
type ApplyActionsInput struct {
	ID   string `path:"id" format:"uuid"`
	Body Actions
}

// ApplyActions 在单个事务中原子执行一组会话操作（不发送消息，用于纯操作型快捷回复）。
type ApplyActions struct {
	db   *bun.DB
	down Downstream
}

// NewApplyActions 返回批量执行会话操作的动作；down 是时间线有新内容时通知的下游。
func NewApplyActions(db *bun.DB, down Downstream) *ApplyActions {
	return &ApplyActions{db: db, down: down}
}

// Handle 由 sub 在单个事务中对会话 id 执行 act；act 为空时返回 ErrInvalidActions。
func (a *ApplyActions) Handle(ctx context.Context, sub identity.Subject, id string, act Actions) (*ConversationDTO, error) {
	if !act.HasChanges() {
		return nil, ErrInvalidActions.New()
	}
	return changeTx(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx database.Tx, c *Conversation) ([]*Item, error) {
		return applyActions(ctx, tx, a.down, sub, c, act)
	})
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/actions。
func (a *ApplyActions) HTTP(ctx context.Context, in *ApplyActionsInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
