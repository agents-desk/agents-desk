package team

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// MemberInput 是指定团队收件箱中一个成员的请求。
type MemberInput struct {
	ID       string `path:"id" format:"uuid"`
	MemberID string `path:"member_id" format:"uuid"`
}

// PutMember 把工作区的成员加入团队收件箱，已经在团队收件箱中时保持原样。
type PutMember struct {
	db     *bun.DB
	logins *login.Realm
	files  *filesystem.Service
}

// NewPutMember 返回加入团队收件箱的动作。
func NewPutMember(db *bun.DB, logins *login.Realm, files *filesystem.Service) *PutMember {
	return &PutMember{db: db, logins: logins, files: files}
}

// Handle 把成员 memberID 加入 sub 所在工作区的团队收件箱 id，新加入时记录操作日志，返回团队收件箱中的这个成员。
// 团队收件箱不存在时返回 ErrNotFound；他不是这个工作区的成员时返回 member.ErrNotFound。
func (a *PutMember) Handle(ctx context.Context, sub identity.Subject, id, memberID string) (*TeamMember, error) {
	var m *member.Member
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := find(ctx, tx, sub.Tenant, id, true); err != nil {
			return err
		}
		ref := identity.Ref{Type: member.Type, ID: memberID}
		if _, err := member.RoleOf(ctx, authz.NewStore(tx, auth.Catalog), ref, sub.Tenant); err != nil {
			return err
		}
		// 插入不经过 Timestamps 的钩子，时间自己写；已经在团队收件箱中时不插入
		now := time.Now().UTC().Truncate(time.Microsecond)
		row := &Membership{TeamID: id, MemberID: memberID}
		row.CreatedAt, row.UpdatedAt = now, now
		res, err := tx.NewInsert().Model(row).On("CONFLICT (team_id, member_id) DO NOTHING").Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "save team membership")
		}
		added, err := res.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "save team membership")
		}
		if added == 1 {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event: "team.member_added", TargetType: "team", TargetID: id, Properties: map[string]any{"member_id": memberID},
			}); err != nil {
				return err
			}
		}
		m, err = member.Find(ctx, tx, memberID)
		return err
	})
	if err != nil {
		return nil, err
	}
	statuses, err := member.StatusesOf(ctx, a.db, sub.Tenant, []string{memberID})
	if err != nil {
		return nil, err
	}
	item, err := toTeamMember(ctx, authz.NewStore(a.db, auth.Catalog), a.logins, a.files, sub.Tenant, m, statuses[memberID])
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// HTTP 处理 PUT /t/{slug}/api/teams/{id}/members/{member_id}。
func (a *PutMember) HTTP(ctx context.Context, in *MemberInput) (*TeamMemberOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.MemberID)
	if err != nil {
		return nil, err
	}
	return &TeamMemberOutput{Body: *item}, nil
}
