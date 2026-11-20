package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// BulkAssignInput 是批量指派的请求：会话和指派的内容。
type BulkAssignInput struct {
	Body struct {
		BulkIDs
		Assignment
	}
}

// BulkAssign 批量指派会话，规则与 Assign 相同。
type BulkAssign struct {
	assign *Assign
}

// NewBulkAssign 返回批量指派的动作。
func NewBulkAssign(db *bun.DB, down Downstream) *BulkAssign {
	return &BulkAssign{assign: NewAssign(db, down)}
}

// Handle 由 sub 按 p 依次指派 ids 中的会话，每个会话的结果单独返回。
func (a *BulkAssign) Handle(ctx context.Context, sub identity.Subject, ids []string, p Assignment) ([]BulkResult, error) {
	return bulk(ctx, ids, func(id string) error {
		_, err := a.assign.Handle(ctx, sub, id, p)
		return err
	})
}

// HTTP 处理 POST /t/{slug}/api/conversations/bulk/assign。
func (a *BulkAssign) HTTP(ctx context.Context, in *BulkAssignInput) (*BulkOutput, error) {
	sub, _ := identity.FromContext(ctx)
	out, err := a.Handle(ctx, sub, in.Body.IDs, in.Body.Assignment)
	if err != nil {
		return nil, err
	}
	return &BulkOutput{Body: out}, nil
}
