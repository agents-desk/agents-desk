import { createFileRoute } from "@tanstack/react-router"
import "../../../reports/reports.css"

// 报表模块布局路由：图标栏一级入口，开启 fullBleed 融入工作台分栏骨架，下挂 5 张基础报表作为二级导航；
// 切换子报表时通过 preserveSearch 自动保留当前时间窗口、粒度与团队/渠道筛选条件。
export const Route = createFileRoute("/t/$slug/reports")({
  staticData: {
    title: "nav.reports",
    icon: "chart",
    order: 3,
    permission: "reports:read",
    fullBleed: true,
    preserveSearch: ["preset", "from", "to", "granularity", "team_id", "channel_id"],
  },
})
