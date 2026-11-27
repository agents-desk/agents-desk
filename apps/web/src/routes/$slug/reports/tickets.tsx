import { createFileRoute } from "@tanstack/react-router"
import { ticketsReportSearch } from "../../../reports/search"
import { TicketsPage } from "../../../reports/TicketsPage"

// 工单报表：工单积压、新建与解决趋势、解决时长及类型/团队/优先级/负责人拆解。
export const Route = createFileRoute("/$slug/reports/tickets")({
  staticData: { title: "nav.reports_tickets", icon: "ticket", order: 3, permission: "reports:read" },
  validateSearch: ticketsReportSearch({ defaultPreset: "7d" }),
  component: () => <TicketsPage route={Route} />,
})
