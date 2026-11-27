import { createFileRoute } from "@tanstack/react-router"
import { BusinessPage } from "../../../reports/BusinessPage"
import { businessReportSearch } from "../../../reports/search"

// 商务报表：整合个人商务渠道沟通（网页链接与 Telegram 个人号）及商务机器人/商务群服务指标。
export const Route = createFileRoute("/$slug/reports/business")({
  staticData: { title: "nav.reports_business", icon: "shop", order: 3, permission: "reports:read" },
  validateSearch: businessReportSearch({ defaultPreset: "7d" }),
  component: () => <BusinessPage route={Route} />,
})
