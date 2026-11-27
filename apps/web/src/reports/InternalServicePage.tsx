// 内部服务报表：面向企业微信机器人、Telegram 内部机器人及内部外部群的请求量与响应时效分析。

import { SegmentedControl } from "@mantine/core"
import { useNavigate } from "@tanstack/react-router"
import { type PeriodRoute, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { ConversationsPage } from "./ConversationsPage"
import { ResponseTimePage } from "./ResponseTimePage"
import type { InternalServiceReportSearchParams } from "./search"

type InternalTab = "volume" | "response"

/** 内部服务报表页面组件，整合请求量与响应时效两个切面。 */
export function InternalServicePage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const search = route.useSearch() as InternalServiceReportSearchParams
  const tab: InternalTab = search.tab ?? "volume"

  const setTab = (next: string) => {
    void navigate({
      to: ".",
      search: ((prev: Record<string, unknown>) => {
        const out: Record<string, unknown> = { ...prev }
        if (next === "volume") delete out.tab
        else out.tab = next
        return out
      }) as never,
      replace: true,
    })
  }

  const switcher = (
    <SegmentedControl
      size="xs"
      value={tab}
      onChange={setTab}
      data={[
        { value: "volume", label: t("reports.internal_tab_volume") },
        { value: "response", label: t("reports.service_tab_response") },
      ]}
    />
  )

  const title = t("nav.reports_internal")
  if (tab === "response") {
    return <ResponseTimePage route={route} audience="internal" title={title} toolbarRight={switcher} />
  }
  return <ConversationsPage route={route} audience="internal" title={title} toolbarRight={switcher} />
}
