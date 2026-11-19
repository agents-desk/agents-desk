package member

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// IDInput 是指定一个成员的请求：路径中的成员 ID。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Get 读取当前工作区中的一个成员。
type Get struct {
	db     *bun.DB
	roles  *authz.Store
	logins *login.Realm
	files  *filesystem.Service
}

// NewGet 返回读取工作区成员的动作。
func NewGet(db *bun.DB, roles *authz.Store, logins *login.Realm, files *filesystem.Service) *Get {
	return &Get{db: db, roles: roles, logins: logins, files: files}
}

// Handle 返回 sub 所在工作区中 ID 为 id 的成员。他不是这个工作区的成员时返回 ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*TenantMember, error) {
	role, err := RoleOf(ctx, a.roles, identity.Ref{Type: Type, ID: id}, sub.Tenant)
	if err != nil {
		return nil, err
	}
	m, err := Find(ctx, a.db, id)
	if err != nil {
		return nil, err
	}
	item, err := toTenantMember(ctx, a.logins, a.files, sub, m, role)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// HTTP 处理 GET /t/{slug}/api/members/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*TenantMemberOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &TenantMemberOutput{Body: *item}, nil
}
