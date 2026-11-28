package adminnotify

import (
	"context"
	"log/slog"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/ratelimit"

	"agents-desk/internal/platformadmin"
)

// AlertNotifier 向全平台活跃管理员广播主动告警站内通知，并按 alertKey 施加冷却防风暴。
type AlertNotifier struct {
	db      *bun.DB
	hub     *broadcast.Hub
	limiter ratelimit.Limiter
}

// NewAlertNotifier 创建管理员主动告警通知器；limiter 可选，为 nil 时不做冷却抑制。
func NewAlertNotifier(db *bun.DB, hub *broadcast.Hub, limiter ratelimit.Limiter) *AlertNotifier {
	return &AlertNotifier{db: db, hub: hub, limiter: limiter}
}

// Notify 检查 alertKey 是否处于冷却窗口，未受限时在事务中向所有未停用平台管理员发送站内告警并实时推送。
func (a *AlertNotifier) Notify(ctx context.Context, alertKey string, n notification.Notification) error {
	if a == nil || a.db == nil {
		return nil
	}
	if a.limiter != nil && alertKey != "" {
		if err := a.limiter.Allow(ctx, alertKey); err != nil {
			if errors.Is(err, ratelimit.ErrExceeded) {
				return nil
			}
			return errors.Wrap(err, "check admin alert cooldown")
		}
	}
	var adminIDs []string
	if err := a.db.NewSelect().Table("platform_admins").Column("id").Where("disabled_at IS NULL").Scan(ctx, &adminIDs); err != nil {
		return errors.Wrap(err, "load active platform admins for alert")
	}
	if len(adminIDs) == 0 {
		return nil
	}
	recipients := make([]notification.Recipient, len(adminIDs))
	for i, id := range adminIDs {
		recipients[i] = notification.Recipient{Ref: identity.Ref{Type: platformadmin.Type, ID: id}}
	}
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		_, err := Send(ctx, tx, a.hub, n, recipients...)
		return err
	})
}

// NotifyDeadJob 在队列任务进入失败表（死信）时向平台管理员发送带冷却抑制的告警。
func (a *AlertNotifier) NotifyDeadJob(ctx context.Context, topic string, cause error) {
	if a == nil {
		return
	}
	reason := ""
	if cause != nil {
		reason = cause.Error()
		if r := []rune(reason); len(r) > 160 {
			reason = string(r[:160]) + "…"
		}
	}
	if err := a.Notify(ctx, "dead:"+topic, notification.Notification{
		Event: "alert.queue_dead_letter",
		Data: map[string]any{
			"topic":  topic,
			"reason": reason,
			"href":   "/ops/queue",
		},
	}); err != nil {
		slog.WarnContext(ctx, "send dead job admin alert failed", "topic", topic, "error", err)
	}
}

// MonitorHealth 执行定时健康巡检：检查运行环境自检失败项、近 15 分钟 LLM 错误激增与失败队列激增并发送告警。
func MonitorHealth(ctx context.Context, db *bun.DB, notifier *AlertNotifier, runDoctor func(context.Context) doctor.DoctorReport, at time.Time) error {
	if db == nil || notifier == nil {
		return nil
	}
	windowStart := at.Add(-15 * time.Minute)

	// 1. 运行环境自检失败检查
	if runDoctor != nil {
		rep := runDoctor(ctx)
		var failedCount int
		var firstSummary string
		for _, res := range rep.Results {
			if res.Status == doctor.Failed {
				failedCount++
				if firstSummary == "" {
					firstSummary = res.Summary
				}
			}
		}
		if failedCount > 0 {
			if err := notifier.Notify(ctx, "doctor_failed", notification.Notification{
				Event: "alert.doctor_failed",
				Data: map[string]any{
					"count":   failedCount,
					"summary": firstSummary,
					"href":    "/ops/doctor",
				},
			}); err != nil {
				return err
			}
		}
	}

	// 2. 近 15 分钟 LLM 调用错误率激增（至少 5 次失败且失败率 >= 50%）
	var llmStats struct {
		Total  int `bun:"total"`
		Failed int `bun:"failed"`
	}
	if err := db.NewSelect().Table("llm_calls").
		ColumnExpr("COUNT(*) AS total").
		ColumnExpr("COUNT(*) FILTER (WHERE error <> '') AS failed").
		Where("created_at >= ?", windowStart).
		Scan(ctx, &llmStats); err != nil {
		return errors.Wrap(err, "monitor llm call errors")
	}
	if llmStats.Failed >= 5 && llmStats.Failed*2 >= llmStats.Total {
		if err := notifier.Notify(ctx, "llm_failure_spike", notification.Notification{
			Event: "alert.llm_failure_spike",
			Data: map[string]any{
				"failed": llmStats.Failed,
				"total":  llmStats.Total,
				"href":   "/settings/model",
			},
		}); err != nil {
			return err
		}
	}

	// 3. 近 15 分钟新增队列失败任务激增（>= 5 条）
	var failedJobs int
	if err := db.NewSelect().Table("queue_failed_jobs").
		ColumnExpr("COUNT(*)").
		Where("created_at >= ?", windowStart).
		Scan(ctx, &failedJobs); err != nil {
		return errors.Wrap(err, "monitor queue failed jobs")
	}
	if failedJobs >= 5 {
		if err := notifier.Notify(ctx, "queue_failed_spike", notification.Notification{
			Event: "alert.queue_failed_spike",
			Data: map[string]any{
				"count": failedJobs,
				"href":  "/ops/queue",
			},
		}); err != nil {
			return err
		}
	}
	return nil
}
