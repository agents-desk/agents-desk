import { createFileRoute } from "@tanstack/react-router"

// 设置中“翻译”一节：语气、术语表。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/t/$slug/settings/_translation")({
  staticData: { title: "settings.group_translation", order: 5 },
})
