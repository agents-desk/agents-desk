package automation

import (
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
)

// RegisterJobs 在 worker 上登记自动化处理会话时间线事件的任务。
func RegisterJobs(w *queue.Worker, db *bun.DB, locker *lock.Locker, down conversation.Downstream) {
	Topic.Handle(w, NewRunner(db, locker, down).Job)
}
