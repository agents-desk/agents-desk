import { createFileRoute } from "@tanstack/react-router"

// 设置中“工作区”一节：常规、成员、角色，以后还有工作时间（A4）。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/t/$slug/settings/_workspace")({
  staticData: { title: "工作区", order: 1 },
})
