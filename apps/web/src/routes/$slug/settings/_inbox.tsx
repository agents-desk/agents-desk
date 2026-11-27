import { createFileRoute } from "@tanstack/react-router"

// 二级导航中的“收件箱”分组，包含团队收件箱、工作时间、指派、快捷回复、内置自动化。
export const Route = createFileRoute("/$slug/settings/_inbox")({
  staticData: { title: "settings.group_inbox", order: 2 },
})
