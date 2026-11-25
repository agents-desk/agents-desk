import { SegmentedControl } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { ActivityLog, Page, useT } from "@go-tenon/ui"
import { useState } from "react"
import { api } from "../../../api"

// 平台操作日志：展示平台域（工作区、平台管理员、平台设置、功能开关）的操作记录表格，支持按对象类型筛选和固定分页。
export const Route = createFileRoute("/_app/ops/audit")({
  staticData: { title: "nav.audit", icon: "file-text", order: 6, permission: "audit:read" },
  component: AuditLog,
})

type TargetFilter = "" | "tenant" | "platform_admin" | "settings" | "feature"

function AuditLog() {
  const t = useT()
  const [targetType, setTargetType] = useState<TargetFilter>("")

  return (
    <Page title={t("nav.audit")} description={t("audit.description")}>
      <ActivityLog
        source={{
          key: ["audit", targetType],
          list: (p) => api.audit.list({ ...p, target_type: targetType || undefined }),
        }}
        toolbar={
          <SegmentedControl
            value={targetType}
            onChange={(v) => setTargetType(v as TargetFilter)}
            data={[
              { value: "", label: t("audit.filter.all") },
              { value: "tenant", label: t("audit.filter.tenant") },
              { value: "platform_admin", label: t("audit.filter.platform_admin") },
              { value: "settings", label: t("audit.filter.settings") },
              { value: "feature", label: t("audit.filter.feature") },
            ]}
          />
        }
      />
    </Page>
  )
}
