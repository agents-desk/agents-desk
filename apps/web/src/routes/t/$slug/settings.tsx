import { createFileRoute } from "@tanstack/react-router"

// 工作区设置：一个分组，不进图标栏，从个人菜单的“设置”进入（RailLayout 的 settings）；其下的页面是二级导航。
// 成员、团队收件箱、角色等设置页在后续的 PR 中加入。
export const Route = createFileRoute("/t/$slug/settings")({
  staticData: { title: "设置", icon: "settings", order: 9 },
})
