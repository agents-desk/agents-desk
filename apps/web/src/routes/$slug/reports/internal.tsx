import { createFileRoute } from "@tanstack/react-router"
import { InternalServicePage } from "../../../reports/InternalServicePage"
import { internalServiceReportSearch } from "../../../reports/search"

// 内部服务报表：面向企业微信、Telegram 内部机器人与内部外部群的请求量与响应时效分析。
export const Route = createFileRoute("/$slug/reports/internal")({
  staticData: { title: "nav.reports_internal", icon: "team", order: 4, permission: "reports:read" },
  validateSearch: internalServiceReportSearch({ defaultPreset: "7d" }),
  component: () => <InternalServicePage route={Route} />,
})
