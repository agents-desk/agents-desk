import { SegmentedControl, Stack } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { ActivityTimeline, Page, useT } from "@go-tenon/ui"
import { useState } from "react"
import { api } from "../../../api"

// 平台操作日志：展示平台域（工作区、平台管理员、平台设置）的操作记录时间线，支持按对象类型筛选和分页加载。
export const Route = createFileRoute("/_app/ops/audit")({
  staticData: { title: "nav.audit", icon: "file-text", order: 4, permission: "audit:read" },
  component: AuditLog,
})

type TargetFilter = "" | "tenant" | "platform_admin" | "settings"

function AuditLog() {
  const t = useT()
  const [targetType, setTargetType] = useState<TargetFilter>("")

  return (
    <Page title={t("nav.audit")} description={t("audit.description")}>
      <Stack gap="md">
        <SegmentedControl
          value={targetType}
          onChange={(v) => setTargetType(v as TargetFilter)}
          data={[
            { value: "", label: t("audit.filter.all") },
            { value: "tenant", label: t("audit.filter.tenant") },
            { value: "platform_admin", label: t("audit.filter.platform_admin") },
            { value: "settings", label: t("audit.filter.settings") },
          ]}
          w="fit-content"
        />
        <ActivityTimeline
          source={{
            key: ["audit", targetType],
            list: (before) =>
              api.audit
                .list({ before, target_type: targetType || undefined })
                .then((r) => r.items),
          }}
        />
      </Stack>
    </Page>
  )
}
