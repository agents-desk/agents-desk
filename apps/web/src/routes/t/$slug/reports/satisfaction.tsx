import { createFileRoute } from "@tanstack/react-router"
import { SatisfactionPage } from "../../../../reports/SatisfactionPage"
import { satisfactionSearch } from "../../../../reports/search"

// 满意度报表：CSAT、星级分布、趋势及分页评价明细。
export const Route = createFileRoute("/t/$slug/reports/satisfaction")({
  staticData: { title: "nav.reports_satisfaction", icon: "star", order: 3, permission: "reports:read" },
  validateSearch: satisfactionSearch({ defaultPreset: "7d" }),
  component: () => <SatisfactionPage route={Route} />,
})
