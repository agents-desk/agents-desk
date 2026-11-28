import { Select } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { ActivityLog, Page, useT } from "@go-tenon/ui"
import { useState } from "react"
import { tenantApi } from "../../../../api"

// 工作区操作日志：按对象类型筛选查看当前工作区成员与代登管理员的治理操作记录。
export const Route = createFileRoute("/$slug/settings/_workspace/audit")({
  staticData: { title: "nav.audit", icon: "file-text", order: 5, permission: "tenant-settings:manage" },
  component: WorkspaceAuditLogPage,
})

type TargetFilter =
  | ""
  | "tenant"
  | "member"
  | "role"
  | "team"
  | "channel"
  | "contact"
  | "organization"
  | "blocked_ip"
  | "tag"
  | "attribute"
  | "ticket_type"
  | "macro"
  | "automation_rule"
  | "external_group"
  | "settings"

function WorkspaceAuditLogPage() {
  const { slug } = Route.useParams()
  const t = useT()
  const [targetType, setTargetType] = useState<TargetFilter>("")

  return (
    <Page title={t("nav.audit")} description={t("audit.description")}>
      <ActivityLog
        source={{
          key: ["tenant-audit", slug, targetType],
          list: (p) => tenantApi.tenant.auditList({ ...p, target_type: targetType || undefined }),
        }}
        toolbar={
          <Select
            w={220}
            aria-label={t("audit.filter_label")}
            value={targetType}
            onChange={(v) => setTargetType((v as TargetFilter) ?? "")}
            allowDeselect={false}
            data={[
              { value: "", label: t("audit.filter_all") },
              { value: "member", label: t("audit.filter_member") },
              { value: "role", label: t("audit.filter_role") },
              { value: "team", label: t("audit.filter_team") },
              { value: "channel", label: t("audit.filter_channel") },
              { value: "contact", label: t("audit.filter_contact") },
              { value: "organization", label: t("audit.filter_organization") },
              { value: "blocked_ip", label: t("audit.filter_blocked_ip") },
              { value: "tag", label: t("audit.filter_tag") },
              { value: "attribute", label: t("audit.filter_attribute") },
              { value: "ticket_type", label: t("audit.filter_ticket_type") },
              { value: "macro", label: t("audit.filter_macro") },
              { value: "automation_rule", label: t("audit.filter_automation_rule") },
              { value: "external_group", label: t("audit.filter_external_group") },
              { value: "settings", label: t("audit.filter_settings") },
              { value: "tenant", label: t("audit.filter_tenant") },
            ]}
          />
        }
      />
    </Page>
  )
}
