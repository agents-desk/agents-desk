import { createFileRoute } from "@tanstack/react-router"

// 设置中“个人”一节：个人资料、偏好设置、通知。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/$slug/settings/_personal")({
  staticData: { title: "settings.group_personal", order: 0 },
})
