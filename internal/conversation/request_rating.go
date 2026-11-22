package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// RequestRating 由系统（例如自动化）在会话关闭后向客户发出评价邀请。
type RequestRating struct {
	db   *bun.DB
	down Downstream
}

// NewRequestRating 返回向客户发出评价邀请的动作；down 用于推送给客户的聊天窗口和成员。
func NewRequestRating(db *bun.DB, down Downstream) *RequestRating {
	return &RequestRating{db: db, down: down}
}

// Handle 以系统主体 sub 向已关闭的会话 id 发出评价邀请：记下 rating_requested_at，在时间线中追加仅成员可见的
// conversation.rating_requested，并推送给客户的聊天窗口。会话已经不是关闭状态（例如客户刚刚回复重新打开了），
// 或者这次关闭已经邀请过时什么也不做，返回 false。只有系统主体可以调用，否则返回 ErrForbidden。
func (a *RequestRating) Handle(ctx context.Context, sub identity.Subject, id string) (bool, error) {
	if err := authorize(sub, ""); err != nil {
		return false, err
	}
	requested := false
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if c.Status != Closed || c.ratingRequestedSinceClosed() {
			return nil
		}
		at := now()
		if c.ClosedAt != nil && at.Before(*c.ClosedAt) {
			at = *c.ClosedAt
		}
		c.RatingRequestedAt = &at
		requested = true
		return commit(ctx, tx, a.down, c, &Item{
			Kind: Event, ActorType: SystemActor, ActorID: sub.ID, Visibility: MembersOnly,
			Content: Content{Event: "conversation.rating_requested"},
		})
	})
	return requested, err
}
