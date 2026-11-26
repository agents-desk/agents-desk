import { createFileRoute } from "@tanstack/react-router"

// 工单类型布局路由：列表页和单个工单类型的字段配置页在菜单中保持同一选中项。
export const Route = createFileRoute("/t/$slug/settings/_tickets/ticket-types")({
  staticData: { title: "nav.ticket_types", icon: "ticket", order: 0, permission: "ticket-types:manage" },
})
