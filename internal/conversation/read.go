package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// ReadInput 是标记已读的请求：读到时间线中序号为 seq 的项。
type ReadInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Seq int64 `json:"seq" minimum:"0" doc:"读到的时间线序号，序号不大于它的项都算已读"`
	}
}

// MarkRead 记下成员在会话中读到的位置，会话列表据此显示未读，“@我”据此计数。
type MarkRead struct {
	db *bun.DB
}

// NewMarkRead 返回标记已读的动作。
func NewMarkRead(db *bun.DB) *MarkRead { return &MarkRead{db: db} }

// Handle 记下成员 sub 在他所在工作区的会话 id 中读到了序号 seq。位置只往前推进，超过时间线最新一项时按最新一项记；
// 已读不写时间线和操作日志。会话不存在或 sub 看不到时返回 ErrNotFound。
func (a *MarkRead) Handle(ctx context.Context, sub identity.Subject, id string, seq int64) error {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return err
	}
	if sub.Type != member.Type {
		return ErrForbidden.New()
	}
	c, err := findVisible(ctx, a.db, sub, id, false)
	if err != nil {
		return err
	}
	_, err = a.db.NewRaw(`INSERT INTO conversation_reads (member_id, conversation_id, last_read_seq, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (member_id, conversation_id) DO UPDATE
		SET last_read_seq = GREATEST(conversation_reads.last_read_seq, EXCLUDED.last_read_seq), updated_at = EXCLUDED.updated_at`,
		sub.ID, c.ID, min(seq, c.LastSeq), now()).Exec(ctx)
	return errors.Wrap(err, "save conversation read")
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/read。
func (a *MarkRead) HTTP(ctx context.Context, in *ReadInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID, in.Body.Seq)
}

// unreadOf 返回会话 ids 中对成员 sub 来说未读的：时间线中有他读到的位置之后的消息或内部备注，并且不是他自己写的。
// sub 不是成员时都算已读。
func unreadOf(ctx context.Context, db bun.IDB, sub identity.Subject, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if sub.Type != member.Type || len(ids) == 0 {
		return out, nil
	}
	var unread []string
	err := db.NewRaw(`SELECT c.id FROM conversations c
		LEFT JOIN conversation_reads r ON r.conversation_id = c.id AND r.member_id = ?
		WHERE c.id IN (?) AND EXISTS (
			SELECT 1 FROM timeline_items t WHERE t.conversation_id = c.id AND t.seq > coalesce(r.last_read_seq, 0)
			AND t.kind IN (?, ?) AND NOT (t.actor_type = ? AND t.actor_id = ?))`,
		sub.ID, bun.In(ids), Message, Note, MemberActor, sub.ID).Scan(ctx, &unread)
	if err != nil {
		return nil, errors.Wrap(err, "load unread conversations")
	}
	for _, id := range unread {
		out[id] = true
	}
	return out, nil
}
