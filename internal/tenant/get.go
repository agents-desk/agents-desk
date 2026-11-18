package tenant

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/login"
)

// IDInput 是路径中带有租户 ID 的请求。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Get 读取一个租户。
type Get struct {
	db     *bun.DB
	logins *login.Realm // 成员的 login.Realm，读取所有者的邮箱
	addr   Addresses
}

// NewGet 返回读取租户的动作。
func NewGet(db *bun.DB, logins *login.Realm, addr Addresses) *Get {
	return &Get{db: db, logins: logins, addr: addr}
}

// HTTP 处理 GET /tenants/{id}，不存在时返回 404。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*TenantOutput, error) {
	t, err := find(ctx, a.db, in.ID, false)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.logins, a.addr, t)
	if err != nil {
		return nil, err
	}
	return &TenantOutput{Body: dto}, nil
}
