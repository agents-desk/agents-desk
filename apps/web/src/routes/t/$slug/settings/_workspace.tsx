import { createFileRoute } from "@tanstack/react-router"

// 设置中“工作区”一节：工作区的常规设置，以后还有成员、团队和角色。没有路径，只是二级导航中的一节。
export const Route = createFileRoute("/t/$slug/settings/_workspace")({
  staticData: { title: "工作区", order: 1 },
})
