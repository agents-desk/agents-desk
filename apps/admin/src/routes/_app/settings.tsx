import { createFileRoute, Outlet } from "@tanstack/react-router"

// 平台设置：菜单中的分组，页面在 settings/ 中。
export const Route = createFileRoute("/_app/settings")({
  staticData: { title: "nav.platform_settings", icon: "settings", order: 3 },
  component: Outlet,
})
