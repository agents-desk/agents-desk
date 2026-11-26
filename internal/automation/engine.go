package automation

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
	"agents-desk/internal/hours"
	"agents-desk/internal/tickettype"
)

// Topic 是自动化响应会话时间线变化的下游，在入口加入会话的 Downstream：时间线每有一项新内容，worker 收到一个任务。
var Topic = queue.Topic[conversation.Appended]("conversation.automate")

// 同一会话的自动化规则串行执行的锁时长。
const (
	lockTTL  = 30 * time.Second
	lockWait = 30 * time.Second
)

// Runner 消费会话时间线事件，按“触发 → 条件 → 动作”执行工作区已启用的自动化规则，并记录执行结果去重。
type Runner struct {
	db        *bun.DB
	locker    *lock.Locker
	sendMsg   *conversation.SendSystemMessage
	reqRating *conversation.RequestRating
}

// NewRunner 返回自动化执行器；locker 保证同一会话的规则按顺序执行，down 是动作写入会话时通知的下游。
func NewRunner(db *bun.DB, locker *lock.Locker, down conversation.Downstream) *Runner {
	return &Runner{
		db:        db,
		locker:    locker,
		sendMsg:   conversation.NewSendSystemMessage(db, down),
		reqRating: conversation.NewRequestRating(db, down),
	}
}

// Job 处理 Topic 的任务。
func (r *Runner) Job(ctx context.Context, p conversation.Appended) error {
	return r.Handle(ctx, p)
}

// Handle 处理时间线新增的一项 p：识别触发时机，按会话加锁，加载工作区已启用的规则，逐条检查幂等与条件并执行动作。
// 不是任何触发时机对应的项时直接返回；某条规则动作返回系统错误时记为 failed 并返回错误，队列随后重试，
// 已经执行成功的规则按 (rule_id, dedup_key) 跳过，不会重复执行。
func (r *Runner) Handle(ctx context.Context, p conversation.Appended) error {
	var it conversation.Item
	err := r.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", p.ItemID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load timeline item for automation")
	}
	trigger, dedupKey, ok, err := classify(ctx, r.db, &it)
	if err != nil || !ok {
		return err
	}

	if r.locker != nil {
		lk, err := r.locker.Wait(ctx, "automation:"+it.ConversationID, lockTTL, lockWait)
		if err != nil {
			return errors.Wrap(err, "lock conversation for automation")
		}
		defer func() { _ = lk.Release(context.WithoutCancel(ctx)) }()
	}

	if err := EnsureBuiltins(ctx, r.db, p.TenantID); err != nil {
		return err
	}
	var rules []Rule
	err = r.db.NewSelect().Model(&rules).
		Where("tenant_id = ? AND trigger = ? AND enabled = true", p.TenantID, trigger).
		OrderExpr("position ASC, id ASC").Scan(ctx)
	if err != nil {
		return errors.Wrap(err, "load automation rules")
	}
	if len(rules) == 0 {
		return nil
	}

	var conv conversation.Conversation
	err = r.db.NewSelect().Model(&conv).Where("id = ? AND tenant_id = ?", it.ConversationID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load conversation for automation")
	}

	fact := NewFact(r.db, p.TenantID, &conv, it.CreatedAt)
	sys := identity.Subject{Type: identity.System, ID: "automation", Tenant: p.TenantID}
	for _, rule := range rules {
		done, err := r.alreadyExecuted(ctx, rule.ID, dedupKey)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		matched, err := Evaluate(ctx, rule.Conditions, fact)
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		if err := r.executeActions(ctx, sys, fact, &it, rule.Actions); err != nil {
			_ = r.recordRun(ctx, p.TenantID, rule.ID, conv.ID, it.ID, dedupKey, RunFailed, err.Error())
			return err
		}
		if err := r.recordRun(ctx, p.TenantID, rule.ID, conv.ID, it.ID, dedupKey, RunExecuted, ""); err != nil {
			return err
		}
	}
	return nil
}

// classify 把时间线的一项归一化为自动化触发时机和去重键：
//   - 客户在会话中发出的第一条消息 → TriggerFirstMessage，去重键 conv:<会话 ID>（每个会话只触发一次）；
//   - 会话关闭的系统事件 → TriggerClosed，去重键 item:<事件 ID>（每次关闭都可触发一次）。
//
// 其他项（成员回复、内部备注、系统消息、其他事件）直接忽略，避免自触发循环。
func classify(ctx context.Context, db bun.IDB, it *conversation.Item) (Trigger, string, bool, error) {
	switch {
	case it.Kind == conversation.Message && it.ActorType == conversation.ContactActor:
		var hasEarlier bool
		err := db.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM timeline_items
			WHERE conversation_id = ? AND kind = ? AND actor_type = ? AND seq < ?
		)`, it.ConversationID, conversation.Message, conversation.ContactActor, it.Seq).Scan(ctx, &hasEarlier)
		if err != nil {
			return "", "", false, errors.Wrap(err, "check earlier contact message")
		}
		if hasEarlier {
			return "", "", false, nil
		}
		return TriggerFirstMessage, "conv:" + it.ConversationID, true, nil
	case it.Kind == conversation.Event && it.Content.Event == "conversation.closed":
		return TriggerClosed, "item:" + it.ID, true, nil
	case it.Kind == conversation.Event && it.Content.Event == "ticket.status_changed":
		// 客户自己发消息触发“等待客户 → 处理中”时不再回发状态通知，避免多余打扰
		if reason, _ := it.Content.Data["reason"].(string); reason == "customer_replied" {
			return "", "", false, nil
		}
		return TriggerTicketStatusChanged, "item:" + it.ID, true, nil
	}
	return "", "", false, nil
}

// alreadyExecuted 报告规则 ruleID 对去重键 dedupKey 是否已经执行成功过。
func (r *Runner) alreadyExecuted(ctx context.Context, ruleID, dedupKey string) (bool, error) {
	var executed bool
	err := r.db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM automation_runs WHERE rule_id = ? AND dedup_key = ? AND status = ?
	)`, ruleID, dedupKey, RunExecuted).Scan(ctx, &executed)
	return executed, errors.Wrap(err, "check automation run")
}

// recordRun 写入或更新一次规则的执行记录。
func (r *Runner) recordRun(ctx context.Context, tenantID, ruleID, conversationID, triggerItemID, dedupKey string, status RunStatus, errMsg string) error {
	at := now()
	run := &Run{
		ID: newID(), TenantID: tenantID, RuleID: ruleID, ConversationID: conversationID,
		TriggerItemID: triggerItemID, DedupKey: dedupKey, Status: status, Error: errMsg,
	}
	run.CreatedAt, run.UpdatedAt = at, at
	_, err := r.db.NewInsert().Model(run).
		On("CONFLICT (rule_id, dedup_key) DO UPDATE").
		Set("status = EXCLUDED.status, error = EXCLUDED.error, updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	return errors.Wrap(err, "record automation run")
}

// executeActions 依次执行规则的动作列表，每个动作都调用对应模块的业务动作。
func (r *Runner) executeActions(ctx context.Context, sys identity.Subject, fact *Fact, triggerItem *conversation.Item, actions []Action) error {
	for _, act := range actions {
		switch act.Type {
		case ActionSendReplyTime:
			h, err := fact.Hours(ctx)
			if err != nil {
				return err
			}
			hs, err := fact.HoursSettings(ctx)
			if err != nil {
				return err
			}
			text, prompt := buildReplyTime(h, hs.ReplyTime, fact.At)
			if _, err := r.sendMsg.Handle(ctx, sys, fact.Conv.ID, text, prompt); err != nil {
				return err
			}
		case ActionRequestContactInfo:
			prompt := &conversation.Prompt{
				Kind:   "collect_contact",
				Params: map[string]any{"fields": []string{"email"}},
			}
			if _, err := r.sendMsg.Handle(ctx, sys, fact.Conv.ID, "请留下你的邮箱，方便我们后续联系你。", prompt); err != nil {
				return err
			}
		case ActionRequestRating:
			if _, err := r.reqRating.Handle(ctx, sys, fact.Conv.ID); err != nil {
				return err
			}
		case ActionNotifyTicketStatus:
			if err := r.notifyTicketStatus(ctx, sys, fact, triggerItem); err != nil {
				return err
			}
		}
	}
	return nil
}

// notifyTicketStatus 向客户可见工单（visibility = all）的全部关联服务型会话发送状态变更通知。
// 每个会话按其客户语言生成纯文本回退（供 Telegram 等外部渠道直接发出），并附带结构化 Prompt 供聊天插件按客户浏览器语言渲染卡片。
func (r *Runner) notifyTicketStatus(ctx context.Context, sys identity.Subject, fact *Fact, triggerItem *conversation.Item) error {
	if fact.Conv.Kind != conversation.Ticket {
		return nil
	}
	var tk struct {
		Number     int64                 `bun:"number"`
		Title      string                `bun:"title"`
		Visibility tickettype.Visibility `bun:"visibility"`
	}
	err := r.db.NewSelect().Table("tickets").Column("number", "title", "visibility").
		Where("conversation_id = ? AND tenant_id = ?", fact.Conv.ID, fact.TenantID).
		Scan(ctx, &tk)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load ticket for status notify")
	}
	if tk.Visibility != tickettype.VisibilityAll {
		return nil
	}
	toStatus, _ := triggerItem.Content.Data["to"].(string)
	if toStatus == "" {
		toStatus = string(fact.Conv.Status)
	}

	targets, err := conversation.ResolveTicketNotifyTargets(ctx, r.db, fact.TenantID, fact.Conv.ID)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	prompt := &conversation.Prompt{
		Kind: "ticket_status_changed",
		Params: map[string]any{
			"ticket_id": fact.Conv.ID,
			"number":    tk.Number,
			"title":     tk.Title,
			"status":    toStatus,
		},
	}
	for _, tc := range targets {
		text := formatTicketStatusText(tc.Language, tk.Number, tk.Title, conversation.TicketStatus(toStatus))
		if _, err := r.sendMsg.Handle(ctx, sys, tc.ConversationID, text, prompt); err != nil {
			return errors.Wrapf(err, "notify ticket status to conversation %s", tc.ConversationID)
		}
	}
	return nil
}

// formatTicketStatusText 按客户语言生成工单状态变更的纯文本通知：中文客户用简体中文，其余语言用英文。
func formatTicketStatusText(language string, number int64, title string, status conversation.TicketStatus) string {
	isZH := strings.HasPrefix(strings.ToLower(strings.TrimSpace(language)), "zh")
	if isZH {
		var label string
		switch status {
		case conversation.TicketSubmitted:
			label = "已提交"
		case conversation.TicketInProgress:
			label = "正在处理中"
		case conversation.TicketWaiting:
			label = "等待您补充信息"
		case conversation.TicketResolved:
			label = "已解决"
		default:
			label = string(status)
		}
		return fmt.Sprintf("工单 #%d（%s）状态已更新为：%s", number, title, label)
	}
	var label string
	switch status {
	case conversation.TicketSubmitted:
		label = "Submitted"
	case conversation.TicketInProgress:
		label = "In progress"
	case conversation.TicketWaiting:
		label = "Waiting for your reply"
	case conversation.TicketResolved:
		label = "Resolved"
	default:
		label = string(status)
	}
	return fmt.Sprintf("Ticket #%d (%s) status updated to: %s", number, title, label)
}

// buildReplyTime 按工作时间 h、回复时间 rt 和消息到达时刻 at 构造告知回复时间的纯文本和结构化提示：
//   - 工作时间内：reply_time_open，携带 reply_time；
//   - 非工作时间：reply_time_closed，算得出下次上班时间时携带 next_open_at（RFC3339）和 time_zone，
//     算不出时（例如全年休息）只传 kind，降级为通用提示。
func buildReplyTime(h hours.Hours, rt hours.ReplyTime, at time.Time) (string, *conversation.Prompt) {
	if h.OpenAt(at) {
		return fmt.Sprintf("我们已收到你的消息，通常会在%s回复。", replyTimeText(rt)), &conversation.Prompt{
			Kind:   "reply_time_open",
			Params: map[string]any{"reply_time": string(rt)},
		}
	}
	next, ok := h.NextOpen(at)
	if !ok {
		return "当前为非工作时间，我们将在恢复工作后尽快回复。", &conversation.Prompt{
			Kind: "reply_time_closed",
		}
	}
	return fmt.Sprintf("当前为非工作时间，我们将在 %s 恢复工作后尽快回复。", next.Format("2006-01-02 15:04")), &conversation.Prompt{
		Kind: "reply_time_closed",
		Params: map[string]any{
			"next_open_at": next.Format(time.RFC3339),
			"time_zone":    h.TimeZone(),
		},
	}
}

// replyTimeText 返回回复时间 rt 的中文纯文本回退说法（供不支持卡片的外部渠道使用）。
func replyTimeText(rt hours.ReplyTime) string {
	switch rt {
	case hours.FewMinutes:
		return "几分钟内"
	case hours.WithinHour:
		return "一小时内"
	case hours.FewHours:
		return "几小时内"
	default:
		return "一天内"
	}
}
