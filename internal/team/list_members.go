package team

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/filesystem/fileapi"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/member"
)

// TeamMember 是团队收件箱中的一个成员：账号和他在工作区中的角色。
type TeamMember struct {
	MemberID  string        `json:"member_id"`
	Name      string        `json:"name"`
	Email     string        `json:"email"`
	AvatarURL string        `json:"avatar_url" doc:"头像的地址；没有头像时为空"`
	RoleName  string        `json:"role_name" doc:"他在工作区中的角色的名称"`
	Status    member.Status `json:"status" doc:"他在这个工作区中的在线状态"`
}

// TeamMemberOutput 是返回团队收件箱中一个成员的响应。
type TeamMemberOutput struct {
	Body TeamMember
}

// MembersOutput 是团队收件箱的全部成员。
type MembersOutput struct {
	Body []TeamMember
}

// ListMembers 列出团队收件箱的成员。
type ListMembers struct {
	db     *bun.DB
	roles  *authz.Store
	logins *login.Realm
	files  *filesystem.Service
}

// NewListMembers 返回列出团队收件箱成员的动作。
func NewListMembers(db *bun.DB, roles *authz.Store, logins *login.Realm, files *filesystem.Service) *ListMembers {
	return &ListMembers{db: db, roles: roles, logins: logins, files: files}
}

// Handle 返回 sub 所在工作区中团队收件箱 id 的成员，按名字排列。团队收件箱不存在时返回 ErrNotFound。
func (a *ListMembers) Handle(ctx context.Context, sub identity.Subject, id string) ([]TeamMember, error) {
	if _, err := find(ctx, a.db, sub.Tenant, id, false); err != nil {
		return nil, err
	}
	var rows []member.Member
	err := a.db.NewSelect().TableExpr("members AS m").ColumnExpr("m.*").
		Join("JOIN team_members AS tm ON tm.member_id = m.id").Where("tm.team_id = ?", id).
		OrderExpr("m.name").Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "list team members")
	}
	ids := make([]string, len(rows))
	for i, m := range rows {
		ids[i] = m.ID
	}
	statuses, err := member.StatusesOf(ctx, a.db, sub.Tenant, ids)
	if err != nil {
		return nil, err
	}
	out := make([]TeamMember, 0, len(rows))
	for i := range rows {
		item, err := toTeamMember(ctx, a.roles, a.logins, a.files, sub.Tenant, &rows[i], statuses[rows[i].ID])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/teams/{id}/members。
func (a *ListMembers) HTTP(ctx context.Context, in *IDInput) (*MembersOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &MembersOutput{Body: items}, nil
}

// toTeamMember 把账号 m 和他的在线状态转换为团队收件箱中的成员：读取邮箱、头像的地址和他在工作区 tenant 中的角色。
func toTeamMember(ctx context.Context, roles *authz.Store, logins *login.Realm, files *filesystem.Service, tenant string, m *member.Member, status member.Status) (TeamMember, error) {
	email, err := member.Email(ctx, logins, m.Ref())
	if err != nil {
		return TeamMember{}, err
	}
	avatar, err := fileapi.URL(ctx, files, m.AvatarFileID)
	if err != nil {
		return TeamMember{}, err
	}
	role, err := member.RoleOf(ctx, roles, m.Ref(), tenant)
	if err != nil {
		return TeamMember{}, err
	}
	return TeamMember{MemberID: m.ID, Name: m.Name, Email: email, AvatarURL: avatar, RoleName: member.RoleName(ctx, role), Status: status}, nil
}
