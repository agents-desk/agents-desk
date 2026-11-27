import { createFileRoute } from "@tanstack/react-router"

// 内部服务渠道布局路由：管理面向企业内部员工的机器人渠道（企业微信机器人、内部 Telegram 机器人及后续钉钉/飞书/Slack）与内部外部群。
export const Route = createFileRoute("/$slug/settings/_service/internal-channels")({
  staticData: { title: "nav.internal_channels", icon: "team", order: 1, permission: "channels:manage" },
})
