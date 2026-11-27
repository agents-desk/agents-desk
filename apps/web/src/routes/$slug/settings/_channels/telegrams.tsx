import { createFileRoute } from "@tanstack/react-router"

// Telegram 机器人的布局：菜单项和权限写在这里，列表（telegrams/index.tsx）和单个渠道详情（telegrams/$id.tsx）都在它下面。
export const Route = createFileRoute("/$slug/settings/_channels/telegrams")({
  staticData: { title: "nav.telegrams", icon: "telegram", order: 1, permission: "channels:manage" },
})
