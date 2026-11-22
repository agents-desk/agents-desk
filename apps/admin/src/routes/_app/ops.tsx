import { createFileRoute, Outlet } from "@tanstack/react-router"

// 系统运维：菜单中的分组，包含队列面板、定时任务面板、运行环境自检与平台操作日志。
export const Route = createFileRoute("/_app/ops")({
  staticData: { title: "nav.ops", icon: "database", order: 4 },
  component: Outlet,
})
