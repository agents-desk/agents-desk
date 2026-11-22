import { createFileRoute } from "@tanstack/react-router"
import { QueuePanel } from "@go-tenon/ui"
import { api } from "../../../api"

// 队列运维面板：展示各 topic 状态、失败任务与底层复制槽/JetStream 状态。
export const Route = createFileRoute("/_app/ops/queue")({
  staticData: { title: "nav.queue", icon: "list", order: 1, permission: "queue:manage" },
  component: () => <QueuePanel api={api.queue} />,
})
