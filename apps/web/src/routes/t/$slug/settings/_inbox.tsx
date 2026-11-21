import { createFileRoute } from "@tanstack/react-router"

// 设置中“收件箱”一节：团队收件箱，以后还有指派（E1）、快捷回复（M1）。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/t/$slug/settings/_inbox")({
  staticData: { title: "settings.group_inbox", order: 2 },
})
