package member

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// Heartbeat 刷新当前成员在当前工作区处于在线状态的最近活跃心跳时间。
type Heartbeat struct {
	db *bun.DB
}

// NewHeartbeat 返回刷新坐席在线心跳的动作。
func NewHeartbeat(db *bun.DB) *Heartbeat { return &Heartbeat{db: db} }

// Handle 仅在成员 sub 已处于 online 状态时更新 last_seen_at，不把已离开状态改回在线。
func (a *Heartbeat) Handle(ctx context.Context, sub identity.Subject) error {
	_, err := a.db.NewUpdate().Model((*memberStatus)(nil)).
		Set("last_seen_at = ?", time.Now().UTC().Truncate(time.Microsecond)).
		Where("tenant_id = ? AND member_id = ? AND status = ?", sub.Tenant, sub.ID, Online).
		Exec(ctx)
	return errors.Wrap(err, "refresh member heartbeat")
}

// HTTP 处理 POST /t/{slug}/api/me/heartbeat。
func (a *Heartbeat) HTTP(ctx context.Context, _ *struct{}) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub)
}
