package attribute

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// Get 读取当前工作区中的一个自定义属性定义。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取属性定义的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 读取 sub 所在工作区中的属性定义 id；不存在时返回 ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*AttributeDTO, error) {
	d, err := Find(ctx, a.db, sub.Tenant, id, false)
	if err != nil {
		return nil, err
	}
	dto := d.DTO()
	return &dto, nil
}

// HTTP 处理 GET /t/{slug}/api/attributes/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*AttributeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &AttributeOutput{Body: *dto}, nil
}
