import { createFileRoute } from "@tanstack/react-router"

// 二级导航中的“客服与渠道”分组，包含客服渠道、商务渠道治理、团队收件箱、工作时间、指派与自动化、快捷回复和工单类型。
export const Route = createFileRoute("/$slug/settings/_service")({
  staticData: { title: "settings.group_service", order: 2 },
})
