import { createFileRoute } from "@tanstack/react-router"
import { ConversationsPage } from "../../../../reports/ConversationsPage"
import { reportFilterSearch } from "../../../../reports/search"

// 会话报表：新建、重开、关闭趋势、实时积压及多维分布。
export const Route = createFileRoute("/t/$slug/reports/conversations")({
  staticData: { title: "nav.reports_conversations", icon: "message", order: 1, permission: "reports:read" },
  validateSearch: reportFilterSearch({ defaultPreset: "7d" }),
  component: () => <ConversationsPage route={Route} />,
})
