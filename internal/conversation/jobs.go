package conversation

import (
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/channel"
)

// RegisterJobs 在 worker 上登记会话的下游任务：把发给对方的消息交给连接器发出（最终失败时经 OnDead 记为失败并推送），
// 以及全文搜索索引的同步与重建。
func RegisterJobs(w *queue.Worker, db *bun.DB, registry channel.Registry, down ...Downstream) {
	deliver := NewDeliver(db, registry, down...)
	DeliverTopic.Handle(w, deliver.Job, queue.OnDead(deliver.OnDead))
	SearchIndex.Handle(w, db, LoadSearchDocs, ScanSearchKeys)
}
