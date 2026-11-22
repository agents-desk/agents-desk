import { createFileRoute, Outlet } from "@tanstack/react-router"

// 系统运维：菜单中的分组，包含队列面板、定时任务面板与运行环境自检。
export const Route = createFileRoute("/_app/ops")({
  staticData: { title: "nav.ops", icon: "database", order: 3 },
  component: Outlet,
})
