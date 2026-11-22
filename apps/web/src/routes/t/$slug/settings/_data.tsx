import { createFileRoute } from "@tanstack/react-router"

// 设置中“数据”一节：标签、自定义属性；后续加上组织。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/t/$slug/settings/_data")({
  staticData: { title: "settings.group_data", order: 4 },
})
