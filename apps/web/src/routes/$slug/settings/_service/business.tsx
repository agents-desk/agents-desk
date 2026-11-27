import { createFileRoute } from "@tanstack/react-router"

// 商务渠道布局路由：统筹管理成员个人商务渠道（网页链接与 Telegram 个人号）、合作方服务 Telegram 机器人与合作群。
export const Route = createFileRoute("/$slug/settings/_service/business")({
  staticData: { title: "nav.business_governance", icon: "link", order: 2, permission: "business-channels:manage" },
})
