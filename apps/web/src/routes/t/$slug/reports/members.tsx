import { createFileRoute } from "@tanstack/react-router"
import { MembersPage } from "../../../../reports/MembersPage"
import { reportFilterSearch } from "../../../../reports/search"

// 成员报表：成员工作量、响应时间与满意度明细。
export const Route = createFileRoute("/t/$slug/reports/members")({
  staticData: { title: "nav.reports_members", icon: "users", order: 4, permission: "reports:read" },
  validateSearch: reportFilterSearch({ defaultPreset: "7d" }),
  component: () => <MembersPage route={Route} />,
})
