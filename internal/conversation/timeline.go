package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// TimelineInput 是读取时间线的请求：从序号 after 之后读，或者以 around 为中心读取前后一段，最多 limit 项。
type TimelineInput struct {
	ID     string `path:"id" format:"uuid"`
	After  int64  `query:"after" minimum:"0" doc:"只读取序号大于它的项；收到推送后用它增量读取"`
	Around int64  `query:"around" minimum:"0" doc:"以这个序号为中心读取前后一段，用于从搜索结果定位较早的消息"`
	Limit  int    `query:"limit" minimum:"1" maximum:"500" default:"100"`
}

// TimelineOutput 是时间线中的一段，按序号从小到大。
type TimelineOutput struct {
	Body []ItemDTO
}

// Timeline 读取会话的时间线。
type Timeline struct {
	db    *bun.DB
	media Media
}

// NewTimeline 返回读取时间线的动作；media 用于填充消息附带的附件地址（测试中不涉及附件时可省略）。
func NewTimeline(db *bun.DB, media ...Media) *Timeline {
	var m Media
	if len(media) > 0 {
		m = media[0]
	}
	return &Timeline{db: db, media: m}
}

// Handle 返回 sub 所在工作区中会话 id 的时间线里序号大于 after 的项，最多 limit 项，按序号从小到大。
// 成员能看到全部的项，包括内部备注；对方发来、还缺 sub 的语言的译文的消息标为 Untranslated。会话不存在或 sub 看不到时返回 ErrNotFound。
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
	dtos, err := toItemDTOs(ctx, a.db, a.media, items)
	if err != nil {
		return nil, err
	}
	language, err := MemberLanguage(ctx, a.db, sub)
	if err != nil {
		return nil, err
	}
	markUntranslated(dtos, language)
	return dtos, nil
}

// HTTP 处理 GET /t/{slug}/api/conversations/{id}/items。
func (a *Timeline) HTTP(ctx context.Context, in *TimelineInput) (*TimelineOutput, error) {
	sub, _ := identity.FromContext(ctx)
	after := in.After
	if in.Around > 0 {
		half := int64(in.Limit) / 2
		if in.Around > half {
			after = in.Around - half - 1
		} else {
			after = 0
		}
	}
	items, err := a.Handle(ctx, sub, in.ID, after, in.Limit)
	if err != nil {
		return nil, err
	}
	return &TimelineOutput{Body: items}, nil
}
