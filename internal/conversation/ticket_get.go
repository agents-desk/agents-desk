package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// GetTicket 读取单个工单的完整详情，包含当前成员可见的关联服务型会话列表。
type GetTicket struct {
	db *bun.DB
}

// NewGetTicket 返回读取单个工单的动作。
func NewGetTicket(db *bun.DB) *GetTicket { return &GetTicket{db: db} }

// Handle 返回 sub 所在工作区中可见的工单 id；不存在、不是工单或不可见时返回 ErrNotFound。
func (a *GetTicket) Handle(ctx context.Context, sub identity.Subject, id string) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	c, _, err := findVisibleTicket(ctx, a.db, sub, id, false)
	if err != nil {
		return nil, err
	}
	dtos, err := toTicketDTOs(ctx, a.db, sub, []Conversation{*c}, true)
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 GET /t/{slug}/api/tickets/{id}。
func (a *GetTicket) HTTP(ctx context.Context, in *IDInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}

// TicketNumberInput 是按工单编号读取工单的请求。
type TicketNumberInput struct {
	Number int64 `path:"number" minimum:"1" doc:"工单在工作区内的递增编号"`
}

// GetTicketByNumber 按工作区内递增编号读取可见工单（供命令面板按 #1024 直达）。
type GetTicketByNumber struct {
	db *bun.DB
}

// NewGetTicketByNumber 返回按编号读取工单的动作。
func NewGetTicketByNumber(db *bun.DB) *GetTicketByNumber { return &GetTicketByNumber{db: db} }

// Handle 按编号 number 读取 sub 所在工作区中可见的工单；不存在或不可见时返回 ErrNotFound。
func (a *GetTicketByNumber) Handle(ctx context.Context, sub identity.Subject, number int64) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	var id string
	err := a.db.NewSelect().Table("tickets").Column("conversation_id").
		Where("tenant_id = ? AND number = ?", sub.Tenant, number).Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "find ticket by number")
	}
	return NewGetTicket(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 GET /t/{slug}/api/tickets/number/{number}。
func (a *GetTicketByNumber) HTTP(ctx context.Context, in *TicketNumberInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Number)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}
