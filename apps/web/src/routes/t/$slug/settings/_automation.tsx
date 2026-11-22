import { createFileRoute } from "@tanstack/react-router"

// 设置中“自动化”一节：内置自动化规则的开关。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/t/$slug/settings/_automation")({
  staticData: { title: "settings.group_automation", order: 6 },
})
