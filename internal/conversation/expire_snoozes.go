package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// ExpireSnoozes 把稍后处理到期的会话重新打开，由定时任务每分钟运行一次。
type ExpireSnoozes struct {
	db   *bun.DB
	down Downstream
}

// NewExpireSnoozes 返回处理稍后处理到期的动作；down 是时间线有新内容时通知的下游。
func NewExpireSnoozes(db *bun.DB, down Downstream) *ExpireSnoozes {
	return &ExpireSnoozes{db: db, down: down}
}

// Handle 以系统的身份重新打开 at 之前稍后处理到期的会话，时间线中追加 conversation.unsnoozed，返回打开的个数。
// 每次最多处理 500 个，剩下的下一分钟继续。读出之后会话已经被重新打开、或者改到了更晚的时间时跳过它。
// at 由调用方传入，SQL 中不用数据库的时钟。
func (a *ExpireSnoozes) Handle(ctx context.Context, at time.Time) (int, error) {
	var due []struct {
		ID       string `bun:"id"`
		TenantID string `bun:"tenant_id"`
	}
	err := a.db.NewSelect().Table("conversations").Column("id", "tenant_id").
		Where("kind = ? AND status = ? AND snoozed_until <= ?", Service, Snoozed, at).OrderExpr("snoozed_until").Limit(500).Scan(ctx, &due)
	if err != nil {
		return 0, errors.Wrap(err, "list due snoozes")
	}
	opened := 0
	for _, d := range due {
		sys := identity.Subject{Type: identity.System, ID: "snooze", Tenant: d.TenantID}
		reopenDue := reopen(sys)
		_, err := change(ctx, a.db, a.down, sys, d.ID, "", func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
			if c.Status != Snoozed || c.SnoozedUntil == nil || c.SnoozedUntil.After(at) {
				return nil, nil
			}
			opened++
			return reopenDue(ctx, tx, c)
		})
		if err != nil {
			return opened, err
		}
	}
	return opened, nil
}
