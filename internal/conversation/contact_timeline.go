package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// TimelineForContact 读取联系人看到的时间线：只有对所有人可见的项，内部备注和系统事件都不在其中。
type TimelineForContact struct {
	db *bun.DB
}

// NewTimelineForContact 返回联系人读取时间线的动作。
func NewTimelineForContact(db *bun.DB) *TimelineForContact { return &TimelineForContact{db: db} }

// Handle 返回联系人 sub 在渠道 channelID 中的会话 id 里序号大于 after、对所有人可见的项，最多 limit 项，按序号从小到大。
// sub 不是联系人时返回 ErrForbidden；会话不存在、属于别人或别的渠道时返回 ErrNotFound。
func (a *TimelineForContact) Handle(ctx context.Context, sub identity.Subject, channelID, id string, after int64, limit int) ([]ContactItemDTO, error) {
	if _, err := findForContact(ctx, a.db, sub, channelID, id, false); err != nil {
		return nil, err
	}
	var items []Item
	err := a.db.NewSelect().Model(&items).Where("conversation_id = ? AND visibility = ? AND seq > ?", id, Everyone, after).
		OrderExpr("seq").Limit(int64(limit)).Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list contact timeline items")
	}
	return toContactItemDTOs(ctx, a.db, items)
}
