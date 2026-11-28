package conversation

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
)

// tenantLifecycleRule 保存某个启用中工作区的会话生命周期阈值配置。
type tenantLifecycleRule struct {
	TenantID string `bun:"tenant_id"`
	Value    int    `bun:"val"`
}

// AutoCloseIdle 关闭各工作区中最后回复来自坐席/系统且超过 auto_close_after_hours 仍未收到客户回复的打开会话，
// 单次最多处理 limit 条，返回实际关闭的会话数量。
func AutoCloseIdle(ctx context.Context, db *bun.DB, down Downstream, at time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	var rules []tenantLifecycleRule
	err := db.NewSelect().
		TableExpr("settings AS s").
		Join("JOIN tenants AS t ON t.id::text = s.tenant").
		ColumnExpr("s.tenant AS tenant_id").
		ColumnExpr("COALESCE((s.payload->>'auto_close_after_hours')::int, 0) AS val").
		Where("s.name = 'assignment' AND s.tenant <> '' AND t.disabled_at IS NULL").
		Where("COALESCE((s.payload->>'auto_close_after_hours')::int, 0) > 0").
		OrderExpr("s.tenant").
		Scan(ctx, &rules)
	if err != nil {
		return 0, errors.Wrap(err, "load auto close settings")
	}

	closed := 0
	for _, r := range rules {
		remaining := limit - closed
		if remaining <= 0 {
			break
		}
		cutoff := at.Add(-time.Duration(r.Value) * time.Hour)
		var convIDs []string
		err := db.NewSelect().Table("conversations").
			Column("id").
			Where("tenant_id = ? AND kind = ? AND status = ? AND spam_at IS NULL AND waiting_since IS NULL AND last_message_at < ?",
				r.TenantID, Service, Open, cutoff).
			OrderExpr("last_message_at ASC, id ASC").
			Limit(int64(remaining)).
			Scan(ctx, &convIDs)
		if err != nil {
			return closed, errors.Wrap(err, "find idle conversations for auto close")
		}
		for _, convID := range convIDs {
			didClose := false
			err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
				c, err := find(ctx, tx, r.TenantID, convID, true)
				if errors.Is(err, ErrNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				if c.Kind != Service || c.Status != Open || c.SpamAt != nil || c.WaitingSince != nil || !c.LastMessageAt.Before(cutoff) {
					return nil
				}
				items, err := applyClose(ctx, tx, down, system(c.TenantID), c)
				if err != nil || len(items) == 0 {
					return err
				}
				items[0].Content.Data["reason"] = "auto_close"
				if err := commit(ctx, tx, down, c, items...); err != nil {
					return err
				}
				didClose = true
				return nil
			})
			if err != nil {
				return closed, err
			}
			if didClose {
				closed++
			}
		}
	}
	if closed > 0 {
		slog.InfoContext(ctx, "auto closed idle conversations", "count", closed)
	}
	return closed, nil
}

// ReassignSLABreached 检查开启了 sla_reassign_minutes 的工作区，将处于轮流团队收件箱、等待客户回复时间超过阈值、
// 且冷却窗口内未被超时改派过的打开会话改派给同团队的另一位在线成员，单次最多处理 limit 条，返回实际改派数量。
func ReassignSLABreached(ctx context.Context, db *bun.DB, down Downstream, at time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	var rules []tenantLifecycleRule
	err := db.NewSelect().
		TableExpr("settings AS s").
		Join("JOIN tenants AS t ON t.id::text = s.tenant").
		ColumnExpr("s.tenant AS tenant_id").
		ColumnExpr("COALESCE((s.payload->>'sla_reassign_minutes')::int, 0) AS val").
		Where("s.name = 'assignment' AND s.tenant <> '' AND t.disabled_at IS NULL").
		Where("COALESCE((s.payload->>'sla_reassign_minutes')::int, 0) > 0").
		OrderExpr("s.tenant").
		Scan(ctx, &rules)
	if err != nil {
		return 0, errors.Wrap(err, "load sla reassign settings")
	}

	reassigned := 0
	for _, r := range rules {
		remaining := limit - reassigned
		if remaining <= 0 {
			break
		}
		cutoff := at.Add(-time.Duration(r.Value) * time.Minute)
		var convIDs []string
		err := db.NewSelect().TableExpr("conversations AS c").
			Join("JOIN teams AS tm ON tm.id = c.team_id AND tm.tenant_id = c.tenant_id").
			Column("c.id").
			Where("c.tenant_id = ? AND c.kind = ? AND c.status = ? AND c.spam_at IS NULL", r.TenantID, Service, Open).
			Where("c.assignee_type = ? AND c.assignee_id <> '' AND c.waiting_since IS NOT NULL AND c.waiting_since < ?", MemberActor, cutoff).
			Where("tm.assignment = ? AND tm.disabled_at IS NULL", "round_robin").
			Where(`NOT EXISTS (
				SELECT 1 FROM timeline_items AS ti
				WHERE ti.tenant_id = c.tenant_id AND ti.conversation_id = c.id AND ti.kind = ?
					AND ti.content->>'event' = 'conversation.assigned'
					AND ti.content->'data'->>'reason' = ?
					AND ti.created_at >= ?
			)`, Event, string(ReasonSLATimeout), cutoff).
			OrderExpr("c.waiting_since ASC, c.id ASC").
			Limit(int64(remaining)).
			Scan(ctx, &convIDs)
		if err != nil {
			return reassigned, errors.Wrap(err, "find sla breached conversations")
		}

		for _, convID := range convIDs {
			didReassign := false
			err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
				c, err := find(ctx, tx, r.TenantID, convID, true)
				if errors.Is(err, ErrNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				if c.Kind != Service || c.Status != Open || c.SpamAt != nil || c.TeamID == nil ||
					c.AssigneeType != MemberActor || c.AssigneeID == "" || c.WaitingSince == nil || !c.WaitingSince.Before(cutoff) {
					return nil
				}
				var teamRow struct {
					Assignment string     `bun:"assignment"`
					DisabledAt *time.Time `bun:"disabled_at"`
				}
				err = tx.NewRaw("SELECT assignment, disabled_at FROM teams WHERE id = ? AND tenant_id = ? FOR UPDATE", *c.TeamID, c.TenantID).Scan(ctx, &teamRow)
				if errors.Is(err, sql.ErrNoRows) || (err == nil && (teamRow.DisabledAt != nil || teamRow.Assignment != "round_robin")) {
					return nil
				}
				if err != nil {
					return errors.Wrap(err, "lock team for sla reassign")
				}
				// 二次核验冷却窗口，避免并发实例重复改派同一会话
				recent, err := tx.NewSelect().Table("timeline_items").
					Where("tenant_id = ? AND conversation_id = ? AND kind = ? AND content->>'event' = 'conversation.assigned' AND content->'data'->>'reason' = ? AND created_at >= ?",
						c.TenantID, c.ID, Event, string(ReasonSLATimeout), cutoff).
					Exists(ctx)
				if err != nil {
					return errors.Wrap(err, "check sla cooldown")
				}
				if recent {
					return nil
				}
				var next string
				err = tx.NewRaw(`SELECT tm.member_id FROM team_members tm
					JOIN member_statuses s ON s.tenant_id = ? AND s.member_id = tm.member_id AND s.status = 'online'
					WHERE tm.team_id = ? AND tm.member_id <> ?
					ORDER BY tm.last_assigned_at NULLS FIRST, tm.member_id LIMIT 1`,
					c.TenantID, *c.TeamID, c.AssigneeID).Scan(ctx, &next)
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				if err != nil {
					return errors.Wrap(err, "pick next online member for sla reassign")
				}
				if _, err := tx.NewRaw("UPDATE team_members SET last_assigned_at = ? WHERE team_id = ? AND member_id = ?", at, *c.TeamID, next).Exec(ctx); err != nil {
					return errors.Wrap(err, "record sla round robin")
				}
				prevID := c.AssigneeID
				c.AssigneeType, c.AssigneeID = MemberActor, next
				ev := assignedEvent(system(c.TenantID), c, c.TeamID, MemberActor, prevID, ReasonSLATimeout)
				if err := commit(ctx, tx, down, c, ev); err != nil {
					return err
				}
				didReassign = true
				return nil
			})
			if err != nil {
				return reassigned, err
			}
			if didReassign {
				reassigned++
			}
		}
	}
	if reassigned > 0 {
		slog.InfoContext(ctx, "reassigned sla breached conversations", "count", reassigned)
	}
	return reassigned, nil
}
