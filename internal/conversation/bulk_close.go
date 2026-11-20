package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// BulkCloseInput 是批量关闭的请求。
type BulkCloseInput struct {
	Body BulkIDs
}

// BulkClose 批量关闭会话，规则与 Close 相同。
type BulkClose struct {
	close *Close
}

// NewBulkClose 返回批量关闭的动作。
func NewBulkClose(db *bun.DB, down Downstream) *BulkClose {
	return &BulkClose{close: NewClose(db, down)}
}

// Handle 由 sub 依次关闭 ids 中的会话，每个会话的结果单独返回。
func (a *BulkClose) Handle(ctx context.Context, sub identity.Subject, ids []string) ([]BulkResult, error) {
	return bulk(ctx, ids, func(id string) error {
		_, err := a.close.Handle(ctx, sub, id)
		return err
	})
}

// HTTP 处理 POST /t/{slug}/api/conversations/bulk/close。
func (a *BulkClose) HTTP(ctx context.Context, in *BulkCloseInput) (*BulkOutput, error) {
	sub, _ := identity.FromContext(ctx)
	out, err := a.Handle(ctx, sub, in.Body.IDs)
	if err != nil {
		return nil, err
	}
	return &BulkOutput{Body: out}, nil
}
