import { createFileRoute } from "@tanstack/react-router"
import "../../../reports/reports.css"

// 报表模块布局路由：图标栏一级入口，下挂概览、客服、工单、商务、成员、AI 与翻译 6 张正交报表；
// 切换子报表时通过 preserveSearch 自动保留当前时间窗口与粒度。
export const Route = createFileRoute("/t/$slug/reports")({
  staticData: {
    title: "nav.reports",
    icon: "chart",
    order: 4,
    permission: "reports:read",
    fullBleed: true,
    preserveSearch: ["preset", "from", "to", "granularity"],
  },
})
