import { periodSearch } from "@go-tenon/ui"
import { createFileRoute } from "@tanstack/react-router"
import { OverviewPage } from "../../../../reports/OverviewPage"

// 报表概览：全工作区实时健康状态与四大业务域核心指标。
export const Route = createFileRoute("/t/$slug/reports/overview")({
  staticData: { title: "nav.reports_overview", icon: "dashboard", order: 1, permission: "reports:read" },
  validateSearch: periodSearch({ defaultPreset: "7d" }),
  component: () => <OverviewPage route={Route} />,
})
