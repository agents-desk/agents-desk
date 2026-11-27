import { createFileRoute } from "@tanstack/react-router"

// 个人商务渠道的布局：菜单项和权限写在这里，列表（business-channels/index.tsx）和各渠道工作台详情都在它下面，
// 进入工作台时菜单中的“我的商务渠道”保持选中。
export const Route = createFileRoute("/$slug/settings/_personal/business-channels")({
  staticData: { title: "nav.business_channels", icon: "link", order: 5, permission: "business-channels:use" },
})
