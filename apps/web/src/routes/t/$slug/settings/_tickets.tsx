import { createFileRoute } from "@tanstack/react-router"

// 二级导航中的“工单”分组，包含工单类型。
export const Route = createFileRoute("/t/$slug/settings/_tickets")({
  staticData: { title: "settings.group_tickets", order: 5 },
})
