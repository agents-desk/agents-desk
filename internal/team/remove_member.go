package team

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// RemoveMember 把一个成员移出团队收件箱，他仍是工作区的成员。
type RemoveMember struct {
	db *bun.DB
}

// NewRemoveMember 返回移出团队收件箱的动作。
func NewRemoveMember(db *bun.DB) *RemoveMember { return &RemoveMember{db: db} }

// Handle 把 memberID 移出 sub 所在工作区的团队收件箱 id，并记录操作日志。团队收件箱不存在时返回 ErrNotFound，他不在团队收件箱中时返回 ErrNotInTeam。
func (a *RemoveMember) Handle(ctx context.Context, sub identity.Subject, id, memberID string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := find(ctx, tx, sub.Tenant, id, true); err != nil {
			return err
		}
		res, err := tx.NewDelete().Model((*Membership)(nil)).Where("team_id = ? AND member_id = ?", id, memberID).Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "remove team member")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotInTeam.New()
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "team.member_removed", TargetType: "team", TargetID: id, Properties: map[string]any{"member_id": memberID},
		})
	})
}

// HTTP 处理 DELETE /t/{slug}/api/teams/{id}/members/{member_id}。
func (a *RemoveMember) HTTP(ctx context.Context, in *MemberInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID, in.MemberID)
}
