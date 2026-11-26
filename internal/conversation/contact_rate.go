package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// RateByContact 是客户评价已关闭的会话：1 到 5 星，可以附一句意见。分数记为会话的满意度评分。
type RateByContact struct {
	db   *bun.DB
	down Downstream
}

// NewRateByContact 返回客户评价会话的动作；down 是时间线有新内容时通知的下游。
func NewRateByContact(db *bun.DB, down Downstream) *RateByContact {
	return &RateByContact{db: db, down: down}
}

// Handle 由联系人 sub 评价他在渠道 channelID 中的会话 id：记下星数 score、意见 comment 和评价的时间，时间线中追加
// 只有成员可见的 conversation.rated。会话还没有关闭时返回 ErrNotClosed；这次关闭已经评价过时返回 ErrAlreadyRated，
// 会话重新打开、再关闭后可以再评价。sub 不是联系人时返回 ErrForbidden；会话不存在、属于别人或别的渠道时返回 ErrNotFound。
// score 的范围由入口校验。
func (a *RateByContact) Handle(ctx context.Context, sub identity.Subject, channelID, id string, score int, comment string) (*ContactConversationMeta, error) {
	var c *Conversation
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if c, err = findForContact(ctx, tx, sub, channelID, id, true); err != nil {
			return err
		}
		switch {
		case c.Status != Closed:
			return ErrNotClosed.New()
		case !c.ratingRequestedSinceClosed():
			return ErrRatingNotRequested.New()
		case c.ratedSinceClosed():
			return ErrAlreadyRated.New()
		}
		at := now()
		var r Round
		err = tx.NewSelect().Model(&r).
			Where("tenant_id = ? AND conversation_id = ? AND closed_at IS NOT NULL", c.TenantID, c.ID).
			OrderExpr("round DESC").Limit(1).For("UPDATE").Scan(ctx)
		if err != nil {
			return errors.Wrap(err, "load latest closed round")
		}
		r.Rating, r.RatingComment, r.RatedAt, r.UpdatedAt = &score, comment, &at, at
		if _, err := tx.NewUpdate().Model(&r).Column("rating", "rating_comment", "rated_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update round rating")
		}
		c.Rating, c.RatingComment, c.RatedAt = &score, comment, &at
		return commit(ctx, tx, a.down, c, &Item{
			Kind: Event, ActorType: ContactActor, ActorID: sub.ID, Visibility: MembersOnly,
			Content: Content{Event: "conversation.rated", Data: map[string]any{"score": score, "comment": comment}},
		})
	})
	if err != nil {
		return nil, err
	}
	meta := toContactConversationMeta(c)
	return &meta, nil
}
