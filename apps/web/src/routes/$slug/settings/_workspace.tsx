import { createFileRoute } from "@tanstack/react-router"

// 二级导航中的“工作区”分组，包含常规、成员与角色、标签与属性、翻译。
export const Route = createFileRoute("/$slug/settings/_workspace")({
  staticData: { title: "settings.group_workspace", order: 1 },
})
