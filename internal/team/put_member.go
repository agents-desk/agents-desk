package team

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// MemberChange 是把成员加入团队、或者修改他是不是组长的请求。
type MemberChange struct {
	Lead bool `json:"lead" doc:"是不是这个团队的组长；只有组长、管理员和主管理员能当"`
}

// MemberInput 是指定团队中一个成员的请求。
type MemberInput struct {
	ID       string `path:"id" format:"uuid"`
	MemberID string `path:"member_id" format:"uuid"`
}

// PutMemberInput 是加入团队或修改组长身份的 HTTP 请求。
type PutMemberInput struct {
	MemberInput
	Body MemberChange
}

// PutMember 把工作区的成员加入团队，已经在团队中时修改他是不是组长。
type PutMember struct {
	db     *bun.DB
	logins *login.Realm
	files  *filesystem.Service
}

// NewPutMember 返回加入团队、修改组长身份的动作。
func NewPutMember(db *bun.DB, logins *login.Realm, files *filesystem.Service) *PutMember {
	return &PutMember{db: db, logins: logins, files: files}
}

// Handle 在 sub 所在工作区的团队 id 中加入成员 memberID 或修改他的组长身份，并记录操作日志。
// 团队不存在时返回 ErrNotFound；他不是这个工作区的成员时返回 member.ErrNotFound；要把坐席设为组长时返回 ErrLeadRole（字段 lead）。
func (a *PutMember) Handle(ctx context.Context, sub identity.Subject, id, memberID string, c MemberChange) (*TeamMember, error) {
	var m *member.Member
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := find(ctx, tx, sub.Tenant, id, true); err != nil {
			return err
		}
		ref := identity.Ref{Type: member.Type, ID: memberID}
		role, err := member.RoleOf(ctx, authz.NewStore(tx, auth.Catalog), ref, sub.Tenant)
		if err != nil {
			return err
		}
		if c.Lead && !canLead(role) {
			return ErrLeadRole.New(errorx.Field("lead"))
		}
		var existing Membership
		err = tx.NewSelect().Model(&existing).Where("team_id = ? AND member_id = ?", id, memberID).Scan(ctx)
		joined := errors.Is(err, sql.ErrNoRows)
		if err != nil && !joined {
			return errors.Wrap(err, "load team membership")
		}
		if !joined && existing.Lead == c.Lead {
			m, err = member.Find(ctx, tx, memberID)
			return err
		}
		// upsert 不经过 Timestamps 的钩子，时间自己写
		now := time.Now().UTC().Truncate(time.Microsecond)
		row := &Membership{TeamID: id, MemberID: memberID, Lead: c.Lead}
		row.CreatedAt, row.UpdatedAt = now, now
		if _, err := tx.NewInsert().Model(row).
			On("CONFLICT (team_id, member_id) DO UPDATE").Set("lead = EXCLUDED.lead, updated_at = EXCLUDED.updated_at").
			Exec(ctx); err != nil {
			return errors.Wrap(err, "save team membership")
		}
		event := "team.member_updated"
		if joined {
			event = "team.member_added"
		}
		if err := activity.Record(ctx, tx, sub, activity.Activity{
			Event: event, TargetType: "team", TargetID: id, Properties: map[string]any{"member_id": memberID, "lead": c.Lead},
		}); err != nil {
			return err
		}
		m, err = member.Find(ctx, tx, memberID)
		return err
	})
	if err != nil {
		return nil, err
	}
	item, err := toTeamMember(ctx, authz.NewStore(a.db, auth.Catalog), a.logins, a.files, sub.Tenant, m, c.Lead)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// HTTP 处理 PUT /t/{slug}/api/teams/{id}/members/{member_id}。
func (a *PutMember) HTTP(ctx context.Context, in *PutMemberInput) (*TeamMemberOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.MemberID, in.Body)
	if err != nil {
		return nil, err
	}
	return &TeamMemberOutput{Body: *item}, nil
}
