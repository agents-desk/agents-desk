package agent

import (
	"context"
	"database/sql"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/ai/chat"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// ApprovalDecisionInput 是工具审批操作的输入，reject 时附带拒绝理由。
type ApprovalDecisionInput struct {
	Reason string `json:"reason,omitempty" maxLength:"500" doc:"拒绝原因（仅拒绝时使用）"`
}

// ApproveToolCall 批准或拒绝正在等待确认的高风险工具调用，并在全部决断完成后恢复运行。
type ApproveToolCall struct {
	db  *bun.DB
	svc *Service
}

// NewApproveToolCall 返回工具审批动作。
func NewApproveToolCall(db *bun.DB, svc *Service) *ApproveToolCall {
	return &ApproveToolCall{db: db, svc: svc}
}

// Approve 由具有回复权限的成员 sub 批准生成 genID 中待审批的工具调用 callID。
func (a *ApproveToolCall) Approve(ctx context.Context, sub identity.Subject, genID, callID string) (*GenerationDTO, error) {
	return a.decide(ctx, sub, genID, callID, true, "")
}

// Reject 由具有回复权限的成员 sub 拒绝生成 genID 中待审批的工具调用 callID，并把理由交回模型。
func (a *ApproveToolCall) Reject(ctx context.Context, sub identity.Subject, genID, callID, reason string) (*GenerationDTO, error) {
	return a.decide(ctx, sub, genID, callID, false, reason)
}

func (a *ApproveToolCall) decide(ctx context.Context, sub identity.Subject, genID, callID string, approve bool, reason string) (*GenerationDTO, error) {
	if sub.Type != member.Type || !sub.Can(string(auth.ConversationsReply)) {
		return nil, ErrForbidden.New()
	}
	if a.svc == nil || a.svc.runs == nil {
		return nil, ErrNotFound.New()
	}
	var g Generation
	if err := a.db.NewSelect().Model(&g).Where("id = ? AND tenant_id = ?", genID, sub.Tenant).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound.New()
		}
		return nil, errors.Wrap(err, "load generation for approval")
	}
	visible, err := conversation.CanView(ctx, a.db, sub, g.ConversationID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrNotFound.New()
	}
	if g.State != WaitingApproval {
		return nil, ErrApprovalNotPending.New()
	}

	foundPending := false
	for _, ap := range g.PendingApprovals {
		if ap.CallID == callID && ap.Status == "pending" {
			foundPending = true
			break
		}
	}
	if !foundPending {
		return nil, ErrApprovalNotPending.New()
	}

	exec, err := a.svc.buildExecutionContext(ctx, sub.Tenant, g.ConversationID, &g)
	if err != nil {
		return nil, err
	}
	runCtx := WithExecutionContext(ctx, exec)

	var (
		nextRun *chat.Run
		runErr  error
	)
	if approve {
		nextRun, runErr = a.svc.runs.Approve(runCtx, sub.Tenant, g.ConversationID, callID)
	} else {
		rejReason := strings.TrimSpace(reason)
		if rejReason == "" {
			rejReason = "Rejected by human agent"
		}
		nextRun, runErr = a.svc.runs.Reject(runCtx, sub.Tenant, g.ConversationID, callID, rejReason)
	}
	if runErr != nil {
		if errors.Is(runErr, chat.ErrNotPending) {
			return nil, ErrApprovalNotPending.New()
		}
		return nil, errors.Wrap(runErr, "resolve tool approval in chat.Runs")
	}

	var updated Generation
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := tx.NewSelect().Model(&updated).Where("id = ? AND tenant_id = ?", genID, sub.Tenant).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return errors.Wrap(err, "lock generation after approval")
		}
		at := database.Now()
		status := "rejected"
		eventName := "generation.tool_rejected"
		if approve {
			status = "approved"
			eventName = "generation.tool_approved"
		}
		pendingLeft := 0
		for i := range updated.PendingApprovals {
			if updated.PendingApprovals[i].CallID == callID {
				updated.PendingApprovals[i].Status = status
				updated.PendingApprovals[i].ReviewedBy = sub.ID
				updated.PendingApprovals[i].ReviewedAt = &at
				updated.PendingApprovals[i].Reason = strings.TrimSpace(reason)
			}
			if updated.PendingApprovals[i].Status == "pending" {
				pendingLeft++
			}
		}
		if nextRun != nil && pendingLeft == 0 {
			updated.State = Streaming
			updated.RunID = nextRun.ID
			restoreMode := conversation.HandlingAIAuto
			if updated.Mode == ModeDraft {
				restoreMode = conversation.HandlingAICopilot
			}
			if err := conversation.SetHandlingModeInTx(ctx, tx, a.svc.down, sub.Tenant, updated.ConversationID, restoreMode, "", false); err != nil {
				return err
			}
		}
		updated.UpdatedAt = at
		if _, err := tx.NewUpdate().Model(&updated).
			Column("state", "run_id", "pending_approvals", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update generation approval state")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: eventName, TargetType: "generation", TargetID: updated.ID,
			Properties: map[string]any{
				"conversation_id": updated.ConversationID,
				"call_id":         callID,
				"reason":          strings.TrimSpace(reason),
			},
		})
	})
	if err != nil {
		return nil, err
	}

	if nextRun != nil {
		gCopy := updated
		a.svc.wg.Add(1)
		go func() {
			defer a.svc.wg.Done()
			_ = a.svc.consumeRun(a.svc.bgCtx, &gCopy, nextRun)
		}()
	}

	dto := updated.dto(conversation.Actor{Type: conversation.BotActor, ID: updated.ActorID})
	return &dto, nil
}
