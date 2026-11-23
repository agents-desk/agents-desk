import { createFileRoute } from "@tanstack/react-router"

// 二级导航中的“工作区”分组，包含常规、成员、角色、标签、属性。
export const Route = createFileRoute("/t/$slug/settings/_workspace")({
  staticData: { title: "settings.group_workspace", order: 1 },
})
