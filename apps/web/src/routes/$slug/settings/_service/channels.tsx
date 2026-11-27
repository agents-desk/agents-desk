import { createFileRoute } from "@tanstack/react-router"

// 客服渠道布局路由：列表页（channels/index.tsx）和各渠道配置工作台都在其下，进入实例详情时菜单保持选中。
export const Route = createFileRoute("/$slug/settings/_service/channels")({
  staticData: { title: "nav.service_channels", icon: "plugin", order: 0, permission: "channels:manage" },
})
