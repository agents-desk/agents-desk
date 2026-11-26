package tickettype

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// Get 读取当前工作区中的一个工单类型。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取工单类型的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 读取 sub 所在工作区中的工单类型 id；不存在或属于别的工作区时返回 ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*TicketTypeDTO, error) {
	t, err := Find(ctx, a.db, sub.Tenant, id, false)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.db, t)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 GET /t/{slug}/api/ticket-types/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*TicketTypeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &TicketTypeOutput{Body: *dto}, nil
}
