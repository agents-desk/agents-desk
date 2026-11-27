import { createFileRoute } from "@tanstack/react-router"
import { MembersPage } from "../../../../reports/MembersPage"
import { reportFilterSearch } from "../../../../reports/search"

// 成员报表：跨客服、工单与商务沟通的团队成员绩效对比。
export const Route = createFileRoute("/t/$slug/reports/members")({
  staticData: { title: "nav.reports_members", icon: "users", order: 5, permission: "reports:read" },
  validateSearch: reportFilterSearch({ defaultPreset: "7d" }),
  component: () => <MembersPage route={Route} />,
})
