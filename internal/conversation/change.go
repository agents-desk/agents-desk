package conversation

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

var (
	// ErrClosed 是会话已经关闭，不能执行这个操作，例如稍后处理一个已关闭的会话。
	ErrClosed = errorx.Define(errorx.Conflict, "conversation.closed")
	// ErrSnoozeInPast 是稍后处理的时间不晚于现在。
	ErrSnoozeInPast = errorx.Define(errorx.InvalidArgument, "conversation.snooze_in_past")
)

// changeFunc 在事务 tx 中修改已锁住的会话 c，返回要追加到时间线的系统事件；返回 nil 表示没有变化，什么也不写。
type changeFunc func(ctx context.Context, tx bun.IDB, c *Conversation) (*Item, error)

// change 是会话操作共用的写入路径：检查权限 perm，在一个事务中锁住 sub 能看到的会话 id，交给 fn 修改，
// 有变化时追加 fn 返回的系统事件、保存会话并发布下游任务（commit），最后返回修改后的会话。
// 会话不存在或 sub 看不到时返回 ErrNotFound，没有权限时返回 ErrForbidden。
func change(ctx context.Context, db *bun.DB, down Downstream, sub identity.Subject, id string, perm auth.Permission, fn changeFunc) (*ConversationDTO, error) {
	if err := authorize(sub, perm); err != nil {
		return nil, err
	}
	var c *Conversation
	err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if c, err = findVisible(ctx, tx, sub, id, true); err != nil {
			return err
		}
		it, err := fn(ctx, tx, c)
		if err != nil || it == nil {
			return err
		}
		return commit(ctx, tx, down, c, it)
	})
	if err != nil {
		return nil, err
	}
	dtos, err := toDTOs(ctx, db, sub, []Conversation{*c})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// event 返回 sub 发起的系统事件 name：只有成员可见，data 是事件的数据。
func event(sub identity.Subject, name string, data map[string]any) *Item {
	return &Item{Kind: Event, ActorType: actorOf(sub), ActorID: sub.ID, Visibility: MembersOnly, Content: Content{Event: name, Data: data}}
}

// timePtr 返回 t 的指针。
func timePtr(t time.Time) *time.Time { return &t }
