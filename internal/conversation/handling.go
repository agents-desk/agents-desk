package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// HandlingModeBody 是切换会话接待控制模式的请求体。
type HandlingModeBody struct {
	Mode           HandlingMode `json:"mode" doc:"目标接待控制模式：human、ai_auto、ai_copilot 或 ai_paused"`
	BotID          string       `json:"bot_id,omitempty" format:"uuid" doc:"启用 AI 模式时绑定的机器人 ID；留空时复用会话已有机器人"`
	Reason         string       `json:"reason,omitempty" maxLength:"100" doc:"切换或转交人工的原因标识"`
	ReleaseToHuman bool         `json:"release_to_human,omitempty" doc:"切回人工时是否将原本由 AI 负责的会话放回团队轮流分配"`
}

// HandlingModeInput 是切换接待控制模式的 HTTP 请求。
type HandlingModeInput struct {
	ID   string `path:"id" format:"uuid"`
	Body HandlingModeBody
}

// SetHandlingMode 显式切换服务型会话的接待控制模式（AI 自动接待、AI 副驾驶、人工暂停或切回人工）。
type SetHandlingMode struct {
	db   *bun.DB
	down Downstream
}

// NewSetHandlingMode 返回切换接待控制模式的业务动作。
func NewSetHandlingMode(db *bun.DB, down Downstream) *SetHandlingMode {
	return &SetHandlingMode{db: db, down: down}
}

// Handle 由 sub 切换工作区中会话 id 的接待控制模式。
func (a *SetHandlingMode) Handle(ctx context.Context, sub identity.Subject, id string, body HandlingModeBody) (*ConversationDTO, error) {
	return changeTx(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx database.Tx, c *Conversation) ([]*Item, error) {
		return applyHandlingMode(ctx, tx, a.down, sub, c, body)
	})
}

// HTTP 处理 PUT /t/{slug}/api/conversations/{id}/handling-mode。
func (a *SetHandlingMode) HTTP(ctx context.Context, in *HandlingModeInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}

// SetHandlingModeInTx 由系统主体在已有事务中切换服务型会话的接待控制模式，供 AI 工具与失败自动转人工复用。
func SetHandlingModeInTx(ctx context.Context, tx database.Tx, down Downstream, tenantID, conversationID string, to HandlingMode, handoverReason string, releaseToHuman bool) error {
	c, err := find(ctx, tx, tenantID, conversationID, true)
	if err != nil {
		return err
	}
	sys := system(tenantID)
	items, err := applyHandlingMode(ctx, tx, down, sys, c, HandlingModeBody{
		Mode:           to,
		Reason:         handoverReason,
		ReleaseToHuman: releaseToHuman,
	})
	if err != nil || len(items) == 0 {
		return err
	}
	return commit(ctx, tx, down, c, items...)
}

// CancelActiveGenerationsInTx 在事务中取消会话所有活跃生成，并在提交后通知底层 AI 运行时中止执行。
func CancelActiveGenerationsInTx(ctx context.Context, tx database.Tx, down Downstream, tenantID, conversationID, reason string) ([]string, error) {
	var ids []string
	err := tx.NewRaw(`UPDATE generations
		SET state = 'canceled', cancel_reason = ?, updated_at = ?
		WHERE tenant_id = ? AND conversation_id = ? AND state IN ('pending', 'streaming', 'waiting_approval')
		RETURNING id::text`,
		reason, database.Now(), tenantID, conversationID).Scan(ctx, &ids)
	if err != nil {
		return nil, errors.Wrap(err, "cancel active generations")
	}
	if len(ids) > 0 && down.OnGenerationCanceled != nil {
		cancelRun := down.OnGenerationCanceled
		tx.AfterCommit(func(ctx context.Context) error {
			cancelRun(ctx, tenantID, conversationID)
			return nil
		})
	}
	return ids, nil
}

// applyHandlingMode 在事务 tx 中对已锁住的会话 c 应用接待控制权切换，返回要写入时间线的系统事件。
func applyHandlingMode(ctx context.Context, tx database.Tx, down Downstream, sub identity.Subject, c *Conversation, body HandlingModeBody) ([]*Item, error) {
	if c.Kind != Service {
		return nil, ErrWrongKind.New()
	}
	to := body.Mode
	switch to {
	case HandlingHuman, HandlingAIAuto, HandlingAICopilot, HandlingAIPaused:
	case HandlingWaitingApproval:
		if sub.Type != identity.System {
			return nil, ErrInvalidHandlingMode.New(errorx.Field("mode"))
		}
	default:
		return nil, ErrInvalidHandlingMode.New(errorx.Field("mode"))
	}

	var items []*Item
	from := c.HandlingMode
	if from == "" {
		from = HandlingHuman
	}

	switch to {
	case HandlingAIAuto, HandlingAICopilot:
		botID, err := resolveTargetBotID(ctx, tx, c, body.BotID)
		if err != nil {
			return nil, err
		}
		if err := join(ctx, tx, c.ID, BotActor, botID); err != nil {
			return nil, err
		}
		if to == HandlingAIAuto && (c.AssigneeType != BotActor || c.AssigneeID != botID) {
			fromTeam := c.TeamID
			fromType, fromID := c.AssigneeType, c.AssigneeID
			c.AssigneeType, c.AssigneeID = BotActor, botID
			items = append(items, assignedEvent(sub, c, fromTeam, fromType, fromID, ReasonAIAuto))
		}
	case HandlingHuman, HandlingAIPaused:
		cancelReason := body.Reason
		if cancelReason == "" {
			cancelReason = "human_takeover"
		}
		if _, err := CancelActiveGenerationsInTx(ctx, tx, down, c.TenantID, c.ID, cancelReason); err != nil {
			return nil, err
		}
		if body.Reason != "" || ((to == HandlingHuman || to == HandlingAIPaused) && (from == HandlingAIAuto || from == HandlingWaitingApproval)) {
			handover := body.Reason
			if handover == "" {
				handover = "human_takeover"
			}
			if err := recordRoundHandover(ctx, tx, c.TenantID, c.ID, handover); err != nil {
				return nil, err
			}
		}
		if c.AssigneeType == BotActor {
			if body.ReleaseToHuman {
				ev, err := release(ctx, tx, c, ReasonAIHandover)
				if err != nil {
					return nil, err
				}
				items = append(items, ev...)
			} else if sub.Type == member.Type && to == HandlingHuman {
				fromTeam := c.TeamID
				fromType, fromID := c.AssigneeType, c.AssigneeID
				c.AssigneeType, c.AssigneeID = MemberActor, sub.ID
				items = append(items, assignedEvent(sub, c, fromTeam, fromType, fromID, ReasonManual))
			}
		}
	}

	if from != to {
		c.HandlingMode = to
		data := map[string]any{"from": string(from), "to": string(to)}
		if body.Reason != "" {
			data["reason"] = body.Reason
		}
		items = append(items, event(sub, "conversation.handling_mode_changed", data))
	}
	return items, nil
}

// resolveTargetBotID 解析启用 AI 模式时绑定的启用机器人：优先使用传入的 botID，其次复用会话现有负责人或机器人参与者。
func resolveTargetBotID(ctx context.Context, tx bun.IDB, c *Conversation, requested string) (string, error) {
	if requested != "" {
		ok, err := botEnabledInTenant(ctx, tx, c.TenantID, requested)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrAssigneeBotNotFound.New(errorx.Field("bot_id"))
		}
		return requested, nil
	}
	if c.AssigneeType == BotActor && c.AssigneeID != "" {
		ok, err := botEnabledInTenant(ctx, tx, c.TenantID, c.AssigneeID)
		if err != nil {
			return "", err
		}
		if ok {
			return c.AssigneeID, nil
		}
	}
	var existing string
	err := tx.NewRaw(`SELECT b.id::text
		FROM conversation_participants cp
		JOIN bots b ON b.id::text = cp.participant_id AND b.tenant_id = ?
		WHERE cp.conversation_id = ? AND cp.participant_type = ? AND cp.left_at IS NULL AND b.enabled = true
		ORDER BY b.created_at, b.id LIMIT 1`,
		c.TenantID, c.ID, BotActor).Scan(ctx, &existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", errors.Wrap(err, "resolve conversation bot")
	}
	if existing == "" {
		return "", ErrAssigneeBotNotFound.New(errorx.Field("bot_id"))
	}
	return existing, nil
}

// recordRoundHandover 在当前未关闭服务轮次记录最近一次从 AI 转交人工的原因。
func recordRoundHandover(ctx context.Context, tx bun.IDB, tenantID, conversationID, reason string) error {
	_, err := tx.NewUpdate().Table("conversation_rounds").
		Set("handover_reason = ?", reason).
		Set("updated_at = ?", database.Now()).
		Where("tenant_id = ? AND conversation_id = ? AND closed_at IS NULL", tenantID, conversationID).
		Exec(ctx)
	return errors.Wrap(err, "record round handover reason")
}
