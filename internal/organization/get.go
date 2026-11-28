package organization

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// GetInput 是读取单个客户公司/组织的 HTTP 请求。
type GetInput struct {
	ID string `path:"id" format:"uuid"`
}

// Get 读取工作区中单个客户公司/组织的详情。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取组织的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 返回工作区中组织 id 的详情；不存在或属于其他工作区时返回 ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*DTO, error) {
	o, err := Find(ctx, a.db, sub.Tenant, id)
	if err != nil {
		return nil, err
	}
	dtos, err := buildDTOs(ctx, a.db, sub.Tenant, []Organization{*o})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 GET /t/{slug}/api/organizations/{id}。
func (a *Get) HTTP(ctx context.Context, in *GetInput) (*Output, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &Output{Body: *dto}, nil
}
