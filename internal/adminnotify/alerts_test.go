package adminnotify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/ratelimit"
	"github.com/go-tenon/tenon/ratelimit/pglimit"

	"agents-desk/internal/adminnotify"
	"agents-desk/internal/apptest"
	"agents-desk/internal/platformadmin"
)

// TestAdminAlertNotifierAndHealthMonitor 验证平台管理员主动告警的活跃管理员定向投递、15 分钟冷却抑制、
// 以及自检失败/LLM 失败激增/队列失败激增与死信回调告警触发逻辑。
func TestAdminAlertNotifierAndHealthMonitor(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	creator := identity.Subject{Type: identity.System, ID: "cli"}
	activeAdmin, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, creator, "活跃运维", "active@alert.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	disabledAdmin, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, creator, "已停用运维", "disabled@alert.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platformadmin.NewDisable(env.DB, env.AdminLogins).Handle(ctx, identity.Subject{Type: platformadmin.Type, ID: activeAdmin.ID}, disabledAdmin.ID); err != nil {
		t.Fatal(err)
	}

	limiter := pglimit.New(env.DB, "admin:alert:test", ratelimit.Config{Rate: 1, Interval: 15 * time.Minute, Burst: 1})
	notifier := adminnotify.NewAlertNotifier(env.DB, nil, limiter)

	at := time.Now().UTC().Truncate(time.Microsecond)
	// 插入 5 条近 15 分钟失败的 LLM 调用（100% 失败）及 5 条失败队列任务
	for i := 0; i < 5; i++ {
		if _, err := env.DB.NewRaw(`INSERT INTO llm_calls
			(id, created_at, updated_at, call_type, operation, meta, endpoint, model, request_id, request, response, duration_ms, error)
			VALUES (?, ?, ?, 'translation', 'incoming', '{}'::jsonb, 'https://api.test/v1', 'gpt-4o', '', '{}'::jsonb, '', 10, 'timeout')`,
			uuid.Must(uuid.NewV7()), at.Add(-2*time.Minute), at).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.NewRaw(`INSERT INTO queue_failed_jobs
			(id, created_at, updated_at, topic, payload, metadata, attempts, error)
			VALUES (?, ?, ?, 'conversation.deliver', '\x7b7d', '{}'::jsonb, 3, 'upstream 502')`,
			uuid.Must(uuid.NewV7()), at.Add(-2*time.Minute), at).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	fakeDoctor := func(context.Context) doctor.DoctorReport {
		return doctor.DoctorReport{
			Results: []doctor.DoctorResult{
				{Group: "nats", Check: "stream", Status: doctor.Failed, Summary: "stream offline"},
			},
		}
	}

	// 第一次巡检应触发 doctor_failed、llm_failure_spike 和 queue_failed_spike 共 3 条告警
	if err := adminnotify.MonitorHealth(ctx, env.DB, notifier, fakeDoctor, at); err != nil {
		t.Fatal(err)
	}
	// 第二次立即巡检应受冷却抑制不再重复入库
	if err := adminnotify.MonitorHealth(ctx, env.DB, notifier, fakeDoctor, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// 死信告警再触发 1 条，并验证同一 topic 冷却
	notifier.NotifyDeadJob(ctx, "conversation.deliver", errors.New("webhook gone"))
	notifier.NotifyDeadJob(ctx, "conversation.deliver", errors.New("webhook gone again"))

	activeRecs, err := notification.List(ctx, env.DB, notification.Query{
		Recipient: notification.Recipient{Ref: identity.Ref{Type: platformadmin.Type, ID: activeAdmin.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(activeRecs) != 4 {
		t.Fatalf("活跃管理员应收到 4 条不重复告警（3 巡检 + 1 死信），实际 %d: %+v", len(activeRecs), activeRecs)
	}

	disabledRecs, err := notification.List(ctx, env.DB, notification.Query{
		Recipient: notification.Recipient{Ref: identity.Ref{Type: platformadmin.Type, ID: disabledAdmin.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(disabledRecs) != 0 {
		t.Fatalf("已停用管理员不应收到告警，实际 %d", len(disabledRecs))
	}
}
