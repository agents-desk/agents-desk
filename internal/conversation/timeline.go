package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// TimelineInput 是读取时间线的请求：从序号 after 之后读，最多 limit 项。
type TimelineInput struct {
	ID    string `path:"id" format:"uuid"`
	After int64  `query:"after" minimum:"0" doc:"只读取序号大于它的项；收到推送后用它增量读取"`
	Limit int    `query:"limit" minimum:"1" maximum:"500" default:"100"`
}

// TimelineOutput 是时间线中的一段，按序号从小到大。
type TimelineOutput struct {
	Body []ItemDTO
}

// Timeline 读取会话的时间线。
type Timeline struct {
	db *bun.DB
}

// NewTimeline 返回读取时间线的动作。
func NewTimeline(db *bun.DB) *Timeline { return &Timeline{db: db} }

// Handle 返回 sub 所在工作区中会话 id 的时间线里序号大于 after 的项，最多 limit 项，按序号从小到大。
// 成员能看到全部的项，包括内部备注。会话不存在或 sub 看不到时返回 ErrNotFound。
func (a *Timeline) Handle(ctx context.Context, sub identity.Subject, id string, after int64, limit int) ([]ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if _, err := findVisible(ctx, a.db, sub, id, false); err != nil {
		return nil, err
	}
	var items []Item
	err := a.db.NewSelect().Model(&items).Where("conversation_id = ? AND seq > ?", id, after).
		OrderExpr("seq").Limit(int64(limit)).Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list timeline items")
	}
	return toItemDTOs(ctx, a.db, items)
}

// HTTP 处理 GET /t/{slug}/api/conversations/{id}/items。
func (a *Timeline) HTTP(ctx context.Context, in *TimelineInput) (*TimelineOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub, in.ID, in.After, in.Limit)
	if err != nil {
		return nil, err
	}
	return &TimelineOutput{Body: items}, nil
}
