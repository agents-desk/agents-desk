package contact

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// GetInput 是读取单个联系人的请求。
type GetInput struct {
	ID string `path:"id" format:"uuid"`
}

// Get 读取工作区中的单个联系人资料。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取联系人的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 返回 sub 所在工作区中联系人 id 的完整资料（含渠道身份和会话数），不存在或属于别的工作区时返回 ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*ContactDetailDTO, error) {
	c, err := Find(ctx, a.db, sub.Tenant, id)
	if err != nil {
		return nil, err
	}
	return loadDetail(ctx, a.db, sub.Tenant, c)
}

// HTTP 处理 GET /t/{slug}/api/contacts/{id}。
func (a *Get) HTTP(ctx context.Context, in *GetInput) (*ContactDetailOutput, error) {
	sub, _ := identity.FromContext(ctx)
	d, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ContactDetailOutput{Body: *d}, nil
}
