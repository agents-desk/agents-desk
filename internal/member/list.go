package member

import (
	"context"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/auth"
)

// ListInput 是工作区成员列表的查询条件。
type ListInput struct {
	page.OffsetParams
	Name   string `query:"name" maxLength:"60" doc:"名字包含的文字"`
	RoleID string `query:"role_id" format:"uuid" doc:"只列出这个角色的成员"`
}

// ListOutput 是一页工作区成员。
type ListOutput struct {
	Body page.Offset[TenantMember]
}

// List 列出当前工作区的成员。
type List struct {
	db     *bun.DB
	roles  *authz.Store
	logins *login.Realm
	files  *filesystem.Service
}

// NewList 返回工作区成员列表的动作。
func NewList(db *bun.DB, roles *authz.Store, logins *login.Realm, files *filesystem.Service) *List {
	return &List{db: db, roles: roles, logins: logins, files: files}
}

// Handle 返回 sub 所在工作区（sub.Tenant）的成员，主管理员在前、其余按名字排列，按名字、角色筛选后分页。
// 成员来自 authz 中这个工作区的角色；一个工作区的成员不多，筛选和分页在内存中完成。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[TenantMember], error) {
	assigned, err := a.roles.Members(ctx, sub.Tenant)
	if err != nil {
		return nil, err
	}
	roleByID := map[string]authz.Role{}
	var ids []string
	for _, m := range assigned {
		if m.Subject.Type != Type || len(m.Roles) == 0 {
			continue
		}
		if r := m.Roles[0]; in.RoleID == "" || r.ID == in.RoleID {
			roleByID[m.Subject.ID] = r
			ids = append(ids, m.Subject.ID)
		}
	}
	var rows []Member
	if len(ids) > 0 {
		if err := a.db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "list tenant members")
		}
	}
	if name := strings.ToLower(strings.TrimSpace(in.Name)); name != "" {
		rows = slices.DeleteFunc(rows, func(m Member) bool { return !strings.Contains(strings.ToLower(m.Name), name) })
	}
	slices.SortFunc(rows, func(x, y Member) int {
		if ox, oy := auth.IsOwner(roleByID[x.ID]), auth.IsOwner(roleByID[y.ID]); ox != oy {
			if ox {
				return -1
			}
			return 1
		}
		return strings.Compare(x.Name, y.Name)
	})
	total := int64(len(rows))
	rows = rows[min(in.Offset(), total):min(in.Offset()+in.Limit(), total)]
	items := make([]TenantMember, 0, len(rows))
	for i := range rows {
		item, err := toTenantMember(ctx, a.logins, a.files, sub, &rows[i], roleByID[rows[i].ID])
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	p := page.NewOffset(items, in.OffsetParams, total)
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/members。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
