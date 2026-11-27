import { createFileRoute } from "@tanstack/react-router"
import { BusinessPage } from "../../../reports/BusinessPage"
import { businessReportSearch } from "../../../reports/search"

// 商务沟通报表：对外商务渠道（个人网页链接与 Telegram 个人号）的活跃度与成员拆解。
export const Route = createFileRoute("/$slug/reports/business")({
  staticData: { title: "nav.reports_business", icon: "shop", order: 4, permission: "reports:read" },
  validateSearch: businessReportSearch({ defaultPreset: "7d" }),
  component: () => <BusinessPage route={Route} />,
})
