import { createFileRoute } from "@tanstack/react-router"

// 设置中“个人”一节：个人资料，以后还有偏好设置。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/t/$slug/settings/_personal")({
  staticData: { title: "个人", order: 0 },
})
