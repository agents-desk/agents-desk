import { createFileRoute } from "@tanstack/react-router"
import { ResponseTimePage } from "../../../../reports/ResponseTimePage"
import { reportFilterSearch } from "../../../../reports/search"

// 响应时间报表：首次回复时长（FRT）与解决时长。
export const Route = createFileRoute("/t/$slug/reports/response-time")({
  staticData: { title: "nav.reports_response_time", icon: "clock", order: 2, permission: "reports:read" },
  validateSearch: reportFilterSearch({ defaultPreset: "7d" }),
  component: () => <ResponseTimePage route={Route} />,
})
