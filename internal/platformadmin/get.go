package platformadmin

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// Get 读取单个平台管理员。
type Get struct {
	db     *bun.DB
	logins *login.Realm
}

// NewGet 返回读取平台管理员的动作。
func NewGet(db *bun.DB, logins *login.Realm) *Get { return &Get{db: db, logins: logins} }

// HTTP 处理 GET /admins/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*AdminOutput, error) {
	sub, _ := identity.FromContext(ctx)
	admin, err := find(ctx, a.db, in.ID)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.logins, admin, sub.ID)
	if err != nil {
		return nil, err
	}
	return &AdminOutput{Body: dto}, nil
}
