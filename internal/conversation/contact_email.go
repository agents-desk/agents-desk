package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// RecordContactEmail 在会话时间线中记下客户刚刚留下的邮箱，并推送给能看到这个会话的成员，
// 让收件箱中的联系人详情和时间线实时更新。
type RecordContactEmail struct {
	db   *bun.DB
	down Downstream
}

// NewRecordContactEmail 返回记录客户留下邮箱的动作；down 是时间线有新内容时通知的下游。
func NewRecordContactEmail(db *bun.DB, down Downstream) *RecordContactEmail {
	return &RecordContactEmail{db: db, down: down}
}

// Handle 由联系人 sub 在渠道 channelID 的会话 id 中记录自己留下的邮箱 email，写入仅成员可见的系统事件
// contact.email_provided。sub 不是联系人时返回 ErrForbidden；会话不存在、属于别人或别的渠道时返回 ErrNotFound。
func (a *RecordContactEmail) Handle(ctx context.Context, sub identity.Subject, channelID, id, email string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findForContact(ctx, tx, sub, channelID, id, true)
		if err != nil {
			return err
		}
		return commit(ctx, tx, a.down, c, &Item{
			Kind: Event, ActorType: ContactActor, ActorID: sub.ID, Visibility: MembersOnly,
			Content: Content{Event: "contact.email_provided", Data: map[string]any{"email": email}},
		})
	})
}
