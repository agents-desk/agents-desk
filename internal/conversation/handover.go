package conversation

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
)

var (
	// ErrHandoverTargetRequired 是发起工作交接时未选择接收成员、接收团队或移出操作。
	ErrHandoverTargetRequired = errorx.Define(errorx.InvalidArgument, "conversation.handover_target_required")
	// ErrHandoverSameMember 是将成员的待办会话交接给其本人。
	ErrHandoverSameMember = errorx.Define(errorx.InvalidArgument, "conversation.handover_same_member")
)

// HandoverParams 是工作交接的请求参数。
type HandoverParams struct {
	TargetMemberID string `json:"target_member_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"接收会话与工单的工作区成员 ID"`
	TargetTeamID   string `json:"target_team_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"可选接收团队收件箱 ID；留空时保持会话原团队或与移出同时生效"`
	RemoveAfter    bool   `json:"remove_after,omitempty" doc:"交接完成后是否同时将该成员移出当前工作区"`
}

// HandoverInput 是工作交接的 HTTP 请求。
type HandoverInput struct {
	ID   string `path:"id" format:"uuid"`
	Body HandoverParams
}

// HandoverResult 是工作交接的执行结果。
type HandoverResult struct {
	TransferredConversations int  `json:"transferred_conversations" doc:"转移的未关闭服务型会话数量"`
	TransferredTickets       int  `json:"transferred_tickets" doc:"转移的未解决工单数量"`
	Removed                  bool `json:"removed" doc:"是否已移出工作区"`
}

// HandoverOutput 是工作交接的 HTTP 响应。
type HandoverOutput struct {
	Body HandoverResult
}

// Handover 在同一个事务中将成员负责的未关闭会话与未解决工单批量交接给指定成员或团队收件箱，并按需将该成员移出当前工作区。
type Handover struct {
	db    *bun.DB
	roles *authz.Store
	hooks member.Hooks
	down  Downstream
}

// NewHandover 返回成员工作交接动作。
func NewHandover(db *bun.DB, roles *authz.Store, hooks member.Hooks, down Downstream) *Handover {
	return &Handover{db: db, roles: roles, hooks: hooks, down: down}
}

// Handle 校验权限与目标合法性，批量改派 memberID 名下未关闭服务型会话与未解决工单，可选同时将其移出工作区。
func (a *Handover) Handle(ctx context.Context, sub identity.Subject, memberID string, p HandoverParams) (*HandoverResult, error) {
	if err := authorize(sub, auth.MembersManage); err != nil {
		return nil, err
	}
	if p.TargetMemberID == "" && p.TargetTeamID == "" && !p.RemoveAfter {
		return nil, ErrHandoverTargetRequired.New()
	}
	if p.TargetMemberID == memberID {
		return nil, ErrHandoverSameMember.New(errorx.Field("target_member_id"))
	}
	if p.RemoveAfter && memberID == sub.ID {
		return nil, member.ErrSelf.New()
	}

	var result HandoverResult
	targetRef := identity.Ref{Type: member.Type, ID: memberID}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		roles := a.roles.WithDB(tx)
		currentRole, err := member.RoleOf(ctx, roles, targetRef, sub.Tenant)
		if err != nil {
			return err
		}
		if p.RemoveAfter && auth.IsOwner(currentRole) {
			return member.ErrOwnerProtected.New()
		}
		if p.TargetMemberID != "" {
			recvRef := identity.Ref{Type: member.Type, ID: p.TargetMemberID}
			if _, err := member.RoleOf(ctx, roles, recvRef, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return member.ErrNotFound.New(errorx.Field("target_member_id"))
				}
				return err
			}
		}
		if p.TargetTeamID != "" {
			var teamRow struct {
				DisabledAt *time.Time `bun:"disabled_at"`
			}
			err := tx.NewSelect().Table("teams").Column("disabled_at").
				Where("id = ? AND tenant_id = ?", p.TargetTeamID, sub.Tenant).Scan(ctx, &teamRow)
			if errors.Is(err, sql.ErrNoRows) {
				return team.ErrNotFound.New(errorx.Field("target_team_id"))
			}
			if err != nil {
				return errors.Wrap(err, "check handover target team")
			}
			if teamRow.DisabledAt != nil {
				return team.ErrDisabled.New(errorx.Field("target_team_id"))
			}
		}

		var convs []Conversation
		err = tx.NewSelect().Model(&convs).
			Where("tenant_id = ? AND assignee_type = ? AND assignee_id = ? AND ((kind = ? AND status <> ?) OR (kind = ? AND status <> ?))",
				sub.Tenant, MemberActor, memberID, Service, Closed, Ticket, TicketResolved).
			OrderExpr("id").For("UPDATE").Scan(ctx)
		if err != nil {
			return errors.Wrap(err, "load conversations for handover")
		}

		for i := range convs {
			c := &convs[i]
			fromTeam := c.TeamID
			fromType, fromID := c.AssigneeType, c.AssigneeID
			if p.TargetTeamID != "" {
				tid := p.TargetTeamID
				c.TeamID = &tid
			}
			if p.TargetMemberID != "" {
				c.AssigneeType = MemberActor
				c.AssigneeID = p.TargetMemberID
			} else {
				c.AssigneeType = ""
				c.AssigneeID = ""
			}
			ev := assignedEvent(sub, c, fromTeam, fromType, fromID, ReasonHandover)
			if err := commit(ctx, tx, a.down, c, ev); err != nil {
				return err
			}
			if c.Kind == Ticket {
				result.TransferredTickets++
			} else {
				result.TransferredConversations++
			}
		}

		if p.RemoveAfter {
			if err := member.Leave(ctx, tx, roles, a.hooks, sub.Tenant, memberID); err != nil {
				return err
			}
			result.Removed = true
		}

		return activity.Record(ctx, tx, sub, activity.Activity{
			Event:      "member.handed_over",
			TargetType: string(member.Type),
			TargetID:   memberID,
			Properties: map[string]any{
				"target_member_id":          p.TargetMemberID,
				"target_team_id":            p.TargetTeamID,
				"transferred_conversations": result.TransferredConversations,
				"transferred_tickets":       result.TransferredTickets,
				"removed":                   result.Removed,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "completed member handover",
		"tenant_id", sub.Tenant, "member_id", memberID,
		"conversations", result.TransferredConversations, "tickets", result.TransferredTickets, "removed", result.Removed)
	return &result, nil
}

// HTTP 处理 POST /t/{slug}/api/members/{id}/handover。
func (a *Handover) HTTP(ctx context.Context, in *HandoverInput) (*HandoverOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &HandoverOutput{Body: *res}, nil
}

// RegisterHandoverRoute 在工作区 API（/t/{slug}/api）注册成员离职工作交接接口。
func RegisterHandoverRoute(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, roles *authz.Store, hooks member.Hooks, down Downstream) {
	op := huma.Operation{
		OperationID: "member-handover",
		Method:      http.MethodPost,
		Path:        "/members/{id}/handover",
		Summary:     "交接成员待办会话与工单",
	}
	require(auth.MembersManage)(&op)
	rest.Register(api, op, NewHandover(db, roles, hooks, down).HTTP)
}
