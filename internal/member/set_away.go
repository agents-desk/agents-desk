package member

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// SetAway 把当前工作区中的另一位成员设为离开，例如他下班时忘了切换。只能设为离开：在线由本人确认自己能接待。
type SetAway struct {
	db *bun.DB
}

// NewSetAway 返回把成员设为离开的动作。
func NewSetAway(db *bun.DB) *SetAway { return &SetAway{db: db} }

// Handle 把 sub 所在工作区中的成员 id 设为离开，保留他自己选的“重新分配”，并记录操作日志。他已经离开时什么也不做；
// 他不是这个工作区的成员时返回 ErrNotFound。
func (a *SetAway) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := RoleOf(ctx, authz.NewStore(tx, auth.Catalog), identity.Ref{Type: Type, ID: id}, sub.Tenant); err != nil {
			return err
		}
		current, err := statusOf(ctx, tx, sub.Tenant, id, true)
		if err != nil || current.Status == Away {
			return err
		}
		if err := saveStatus(ctx, tx, sub.Tenant, id, StatusSetting{Status: Away, Reassign: current.Reassign}); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.status_changed", TargetType: string(Type), TargetID: id,
			Properties: map[string]any{"status": Away, "reassign": current.Reassign},
		})
	})
}

// HTTP 处理 POST /t/{slug}/api/members/{id}/away。
func (a *SetAway) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
