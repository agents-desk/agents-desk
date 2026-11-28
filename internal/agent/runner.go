package agent

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
)

// Topic 是时间线有新内容时交给 AI 的下游，在入口中加入会话的 Downstream：时间线每有一项新内容，worker 收到一个任务。
var Topic = queue.Topic[conversation.Appended]("agent.generate")

// Triggers 报告时间线项是否可能触发 AI 回复或副驾驶草稿：
// 外部来信与成员消息均可入队（由 Runner 按会话种类和 handling_mode 进一步精确决策）；
// 机器人自己的消息与系统事件不触发，避免 AI 之间互相死循环回复。
func Triggers(it *conversation.Item) bool {
	return it.Kind == conversation.Message && it.Visibility == conversation.Everyone &&
		(it.ActorType.IsExternal() || it.ActorType == conversation.MemberActor)
}

// Runner 消费时间线事件，按会话 handling_mode 决策直接回复或 Copilot 草稿，
// 执行消息防抖、单活锁控与插话中断策略，并将运行委托给统一的 Service。
type Runner struct {
	db     *bun.DB
	svc    *Service
	open   *Open
	cancel *Cancel
}

// NewRunner 返回 AI 的队列执行器；svc 为 nil 时跳过生成。
func NewRunner(db *bun.DB, _ conversation.Downstream, hub *broadcast.Hub, svc *Service) *Runner {
	var hook func(ctx context.Context, tenantID, convID string)
	if svc != nil {
		hook = svc.CancelRun
	}
	return &Runner{
		db:     db,
		svc:    svc,
		open:   NewOpen(db),
		cancel: NewCancel(db, hub, hook),
	}
}

// RegisterJobs 在 worker 上登记 AI 的生成任务。
func RegisterJobs(w *queue.Worker, r *Runner) {
	Topic.Handle(w, r.Job)
}

// triggerDecision 描述某条消息应由哪个启用机器人、按什么模式生成；shouldRun=false 时静默忽略。
type triggerDecision struct {
	shouldRun bool
	bot       Bot
	mode      Mode
}

// decideTrigger 根据会话种类、状态、接待控制模式和消息来源精确判断是否触发 AI 生成。
func (r *Runner) decideTrigger(ctx context.Context, tenantID, convID string, it *conversation.Item) (triggerDecision, error) {
	var conv struct {
		Kind         conversation.Kind         `bun:"kind"`
		Status       conversation.Status       `bun:"status"`
		HandlingMode conversation.HandlingMode `bun:"handling_mode"`
		AssigneeType *conversation.ActorType   `bun:"assignee_type"`
		AssigneeID   *string                   `bun:"assignee_id"`
	}
	if err := r.db.NewSelect().Table("conversations").
		Column("kind", "status", "handling_mode", "assignee_type", "assignee_id").
		Where("id = ? AND tenant_id = ?", convID, tenantID).
		Scan(ctx, &conv); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return triggerDecision{}, nil
		}
		return triggerDecision{}, errors.Wrap(err, "load conversation for trigger decision")
	}
	if conv.Status == conversation.Closed {
		return triggerDecision{}, nil
	}

	var mode Mode
	switch conv.Kind {
	case conversation.Service:
		// 服务型会话仅对外部客户可见消息触发；成员回复代表人工介入，绝不回环触发 AI。
		if !it.ActorType.IsExternal() {
			return triggerDecision{}, nil
		}
		switch conv.HandlingMode {
		case conversation.HandlingAIAuto, conversation.HandlingWaitingApproval:
			mode = ModeReply
		case conversation.HandlingAICopilot:
			mode = ModeDraft
		default:
			return triggerDecision{}, nil
		}
	case conversation.Chat:
		// 内部与 Bot 的聊天仅在成员发送消息时触发直接回复。
		if it.ActorType != conversation.MemberActor {
			return triggerDecision{}, nil
		}
		mode = ModeReply
	default:
		return triggerDecision{}, nil
	}

	// 优先选择会话当前指派的启用 Bot，否则取参与者或渠道 ai_reception 配置中启用的 Bot。
	bot, ok, err := r.resolveBot(ctx, tenantID, convID, conv.AssigneeType, conv.AssigneeID)
	if err != nil || !ok {
		return triggerDecision{}, err
	}
	return triggerDecision{shouldRun: true, bot: bot, mode: mode}, nil
}

// resolveBot 解析会话应使用的启用机器人。
func (r *Runner) resolveBot(ctx context.Context, tenantID, convID string, assigneeType *conversation.ActorType, assigneeID *string) (Bot, bool, error) {
	if assigneeType != nil && *assigneeType == conversation.BotActor && assigneeID != nil && *assigneeID != "" {
		var b Bot
		err := r.db.NewSelect().Model(&b).
			Where("id = ? AND tenant_id = ? AND enabled = true", *assigneeID, tenantID).
			Scan(ctx)
		if err == nil {
			return b, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Bot{}, false, errors.Wrap(err, "load assigned bot")
		}
	}
	var b Bot
	err := r.db.NewSelect().Model(&b).
		Join("JOIN conversation_participants AS cp ON cp.participant_id = bot.id::text AND cp.participant_type = ?", conversation.BotActor).
		Where("cp.conversation_id = ? AND bot.tenant_id = ? AND bot.enabled = true", convID, tenantID).
		OrderExpr("bot.created_at, bot.id").Limit(1).
		Scan(ctx)
	if err == nil {
		return b, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Bot{}, false, errors.Wrap(err, "find participant bot")
	}
	// 兜底查找服务型会话渠道上的 ai_reception 配置机器人
	err = r.db.NewSelect().Model(&b).
		Join("JOIN conversations AS c ON c.tenant_id = bot.tenant_id AND c.id = ?", convID).
		Join("JOIN channels AS ch ON ch.id = COALESCE(c.reply_channel_id, c.channel_id) AND ch.tenant_id = c.tenant_id").
		Where("bot.tenant_id = ? AND bot.enabled = true AND ch.enabled = true AND ch.archived_at IS NULL AND COALESCE((ch.settings->'ai_reception'->>'enabled')::boolean, false) = true AND ch.settings->'ai_reception'->>'bot_id' = bot.id::text", tenantID).
		Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Bot{}, false, nil
	}
	if err != nil {
		return Bot{}, false, errors.Wrap(err, "find channel reception bot")
	}
	return b, true, nil
}

// Job 处理时间线的一条新内容：执行路由判断、消息防抖、单活槽位争夺与插话控制。
func (r *Runner) Job(ctx context.Context, p conversation.Appended) error {
	if r.svc == nil || r.svc.runs == nil {
		return nil
	}
	var it conversation.Item
	err := r.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", p.ItemID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load trigger item")
	}
	if !Triggers(&it) {
		return nil
	}

	dec, err := r.decideTrigger(ctx, p.TenantID, p.ConversationID, &it)
	if err != nil || !dec.shouldRun {
		return err
	}

	botCfg := dec.bot.Config()
	if botCfg.DebounceMS > 0 {
		timer := time.NewTimer(time.Duration(botCfg.DebounceMS) * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil
		}
		// 若防抖窗口内已有更新的触发消息进入时间线，让位给更新的任务处理合并上下文。
		var newerExists bool
		if err := r.db.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM timeline_items
			WHERE tenant_id = ? AND conversation_id = ? AND seq > ? AND kind = ? AND visibility = ? AND actor_type NOT IN (?, ?)
		)`, p.TenantID, p.ConversationID, it.Seq, conversation.Message, conversation.Everyone, conversation.BotActor, conversation.SystemActor).Scan(ctx, &newerExists); err != nil {
			return errors.Wrap(err, "check newer debounce item")
		}
		if newerExists {
			return nil
		}
	}

	sys := identity.Subject{Type: identity.System, ID: "agent", Tenant: p.TenantID}
	g, err := r.open.Handle(ctx, sys, OpenParams{
		ConversationID: p.ConversationID,
		BotID:          dec.bot.ID,
		TriggerItemID:  p.ItemID,
		TriggerSeq:     it.Seq,
		Mode:           dec.mode,
		PromptVersion:  botCfg.PromptVersion,
	})
	if errors.Is(err, ErrAlreadyActive) {
		return r.handleActiveConflict(ctx, sys, p, &it, dec, botCfg)
	}
	if err != nil {
		return err
	}
	if g.State != Pending {
		return nil
	}

	run, err := r.svc.runs.Notify(ctx, p.TenantID, p.ConversationID)
	if errors.Is(err, conversation.ErrNotFound) {
		return nil
	}
	if err != nil {
		return r.svc.fail.Handle(ctx, sys, FailParams{GenerationID: g.ID, Reason: err.Error()})
	}
	return r.svc.consumeRun(ctx, g, run)
}

// handleActiveConflict 在同一会话已有进行中生成时按 BargeInPolicy 处理客户插话。
func (r *Runner) handleActiveConflict(ctx context.Context, sys identity.Subject, p conversation.Appended, it *conversation.Item, dec triggerDecision, cfg BotSettings) error {
	var active Generation
	err := r.db.NewSelect().Model(&active).
		Where("tenant_id = ? AND conversation_id = ? AND state IN (?)",
			p.TenantID, p.ConversationID, bun.In([]State{Pending, Streaming, WaitingApproval})).
		OrderExpr("created_at DESC").Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load active generation on conflict")
	}

	// 等待人工工具审批期间，仅通知或等待审批完成，不打断审批状态。
	if active.State == WaitingApproval || cfg.BargeInPolicy != BargeInCancelRestart {
		_, notifyErr := r.svc.runs.Notify(ctx, p.TenantID, p.ConversationID)
		if notifyErr != nil && !errors.Is(notifyErr, ErrFinished) {
			// 等待审批时 Notify 返回 chat.ErrAwaitingApproval，保留现有状态等待成员审批。
			return nil
		}
		return nil
	}

	// cancel_restart 策略：取消旧生成并等待底层 Run 结束，再以最新触发重启生成。
	prevRun, _ := r.svc.runs.Get(ctx, p.TenantID, p.ConversationID)
	if err := r.cancel.HandleWithReason(ctx, sys, active.ID, "barge_in"); err != nil && !errors.Is(err, ErrFinished) {
		return err
	}
	if prevRun != nil {
		select {
		case <-prevRun.Done():
		case <-ctx.Done():
			return nil
		}
	}

	g, err := r.open.Handle(ctx, sys, OpenParams{
		ConversationID: p.ConversationID,
		BotID:          dec.bot.ID,
		TriggerItemID:  p.ItemID,
		TriggerSeq:     it.Seq,
		Mode:           dec.mode,
		PromptVersion:  cfg.PromptVersion,
	})
	if err != nil {
		if errors.Is(err, ErrAlreadyActive) {
			return nil
		}
		return err
	}
	if g.State != Pending {
		return nil
	}
	run, err := r.svc.runs.Notify(ctx, p.TenantID, p.ConversationID)
	if err != nil {
		return r.svc.fail.Handle(ctx, sys, FailParams{GenerationID: g.ID, Reason: err.Error()})
	}
	return r.svc.consumeRun(ctx, g, run)
}
