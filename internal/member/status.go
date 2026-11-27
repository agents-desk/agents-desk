package member

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
)

// Status 是成员在一个工作区中的在线状态：在线的成员可以被自动分配会话。
type Status string

const (
	Online Status = "online" // 在线：可以被自动分配会话
	Away   Status = "away"   // 离开：不会被自动分配
)

// Statuses 是全部在线状态，显示文字是翻译文件中 member.status.<状态> 的消息。
var Statuses = enum.New("member.status", enum.Item(Online), enum.Item(Away))

// Schema 让 OpenAPI 中的 Status 只接受上面的取值，并带有显示文字。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

// StatusSetting 是成员在一个工作区中的在线状态，以及离开时是否把他的会话重新分配。
type StatusSetting struct {
	Status   Status `json:"status"`
	Reassign bool   `json:"reassign" doc:"离开时把我的会话重新分配：对方再发来消息时，会话回到团队收件箱重新分配。切回在线后保留这个选择"`
}

// StatusSettingOutput 是返回在线状态的响应。
type StatusSettingOutput struct {
	Body StatusSetting
}

// memberStatus 是 member_statuses 表中的一行：成员在一个工作区中的在线状态。没有这一行就是离开。
type memberStatus struct {
	bun.BaseModel `bun:"table:member_statuses"`

	TenantID   string     `bun:"tenant_id,pk"`
	MemberID   string     `bun:"member_id,pk"`
	Status     Status     `bun:"status,notnull"`
	Reassign   bool       `bun:"reassign,notnull"`
	LastSeenAt *time.Time `bun:"last_seen_at"`
	UpdatedAt  time.Time  `bun:"updated_at,notnull"`
}

// statusOf 返回成员 memberID 在工作区 tenant 中的在线状态，没有设置过时是离开、不重新分配。lock 为 true 时锁住这一行。
func statusOf(ctx context.Context, db bun.IDB, tenant, memberID string, lock bool) (StatusSetting, error) {
	row := new(memberStatus)
	q := db.NewSelect().Model(row).Where("tenant_id = ? AND member_id = ?", tenant, memberID)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusSetting{Status: Away}, nil
	}
	if err != nil {
		return StatusSetting{}, errors.Wrap(err, "load member status")
	}
	return StatusSetting{Status: row.Status, Reassign: row.Reassign}, nil
}

// StatusesOf 返回工作区 tenant 中成员 ids 的在线状态，按成员 ID 查找；没有设置过的是离开。成员列表、团队收件箱的成员用它显示。
func StatusesOf(ctx context.Context, db bun.IDB, tenant string, ids []string) (map[string]Status, error) {
	out := make(map[string]Status, len(ids))
	for _, id := range ids {
		out[id] = Away
	}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []memberStatus
	if err := db.NewSelect().Model(&rows).Where("tenant_id = ? AND member_id IN (?)", tenant, bun.In(ids)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load member statuses")
	}
	for _, r := range rows {
		out[r.MemberID] = r.Status
	}
	return out, nil
}

// saveStatus 在事务 tx 中把成员 memberID 在工作区 tenant 中的在线状态保存为 s；切到在线时同步写入最近心跳时间。
func saveStatus(ctx context.Context, tx bun.IDB, tenant, memberID string, s StatusSetting) error {
	nowUTC := time.Now().UTC().Truncate(time.Microsecond)
	row := &memberStatus{
		TenantID:  tenant,
		MemberID:  memberID,
		Status:    s.Status,
		Reassign:  s.Reassign,
		UpdatedAt: nowUTC,
	}
	if s.Status == Online {
		row.LastSeenAt = &nowUTC
	}
	_, err := tx.NewInsert().Model(row).
		On("CONFLICT (tenant_id, member_id) DO UPDATE").
		Set("status = EXCLUDED.status, reassign = EXCLUDED.reassign, last_seen_at = EXCLUDED.last_seen_at, updated_at = EXCLUDED.updated_at").Exec(ctx)
	return errors.Wrap(err, "save member status")
}

// ExpireIdleOnline 将超过 idleAfter 未上报心跳的在线成员自动置为离开（保留 reassign 设置），并在提交后调用 notify 通知实时连接。
func ExpireIdleOnline(ctx context.Context, db *bun.DB, idleAfter time.Duration, limit int, notify func(ctx context.Context, tenantID, memberID string) error) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	if idleAfter <= 0 {
		idleAfter = 5 * time.Minute
	}
	nowUTC := time.Now().UTC().Truncate(time.Microsecond)
	cutoff := nowUTC.Add(-idleAfter)
	var stale []struct {
		TenantID string `bun:"tenant_id"`
		MemberID string `bun:"member_id"`
	}
	if err := db.NewSelect().Table("member_statuses").
		Column("tenant_id", "member_id").
		Where("status = ? AND COALESCE(last_seen_at, updated_at) < ?", Online, cutoff).
		OrderExpr("COALESCE(last_seen_at, updated_at) ASC").
		Limit(int64(limit)).
		Scan(ctx, &stale); err != nil {
		return 0, errors.Wrap(err, "select idle online members")
	}
	if len(stale) == 0 {
		return 0, nil
	}

	expired := 0
	for _, target := range stale {
		var updated bool
		if err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
			res, err := tx.NewUpdate().Model((*memberStatus)(nil)).
				Set("status = ?", Away).
				Set("updated_at = ?", nowUTC).
				Where("tenant_id = ? AND member_id = ? AND status = ? AND COALESCE(last_seen_at, updated_at) < ?",
					target.TenantID, target.MemberID, Online, cutoff).
				Exec(ctx)
			if err != nil {
				return errors.Wrap(err, "expire idle member status")
			}
			n, err := res.RowsAffected()
			if err != nil {
				return errors.Wrap(err, "expire idle member status")
			}
			if n == 0 {
				return nil
			}
			updated = true
			if notify != nil {
				tenantID, memberID := target.TenantID, target.MemberID
				tx.AfterCommit(func(ctx context.Context) error {
					return notify(ctx, tenantID, memberID)
				})
			}
			return nil
		}); err != nil {
			return expired, err
		}
		if updated {
			expired++
		}
	}
	return expired, nil
}
