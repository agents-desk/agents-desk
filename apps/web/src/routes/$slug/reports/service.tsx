import { createFileRoute } from "@tanstack/react-router"
import { serviceReportSearch } from "../../../reports/search"
import { ServicePage } from "../../../reports/ServicePage"

// 客服报表：整合会话量、响应时效与满意度三个切面。
export const Route = createFileRoute("/$slug/reports/service")({
  staticData: { title: "nav.reports_service", icon: "support", order: 2, permission: "reports:read" },
  validateSearch: serviceReportSearch({ defaultPreset: "7d" }),
  component: () => <ServicePage route={Route} />,
})
