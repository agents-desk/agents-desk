package conversation

import (
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/channel"
)

// RegisterJobs 在 worker 上登记会话的下游任务：把发给对方的消息交给连接器发出。
func RegisterJobs(w *queue.Worker, db *bun.DB, registry channel.Registry) {
	DeliverTopic.Handle(w, NewDeliver(db, registry).Job)
}
