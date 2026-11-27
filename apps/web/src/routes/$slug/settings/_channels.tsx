import { createFileRoute } from "@tanstack/react-router"

// 设置中“渠道”一节：网页聊天插件，以后还有 Telegram、邮件、WhatsApp、群聊机器人。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/$slug/settings/_channels")({
  staticData: { title: "settings.group_channels", order: 3 },
})
