import { createFileRoute } from "@tanstack/react-router"
import { PartnerServicePage } from "../../../reports/PartnerServicePage"
import { partnerServiceReportSearch } from "../../../reports/search"

// 合作方服务报表：面向合作方 Telegram 机器人与合作群的请求量与响应时效分析。
export const Route = createFileRoute("/$slug/reports/partner")({
  staticData: { title: "nav.reports_partner", icon: "link", order: 3, permission: "reports:read" },
  validateSearch: partnerServiceReportSearch({ defaultPreset: "7d" }),
  component: () => <PartnerServicePage route={Route} />,
})
