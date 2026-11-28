// 客服报表统一入口：把会话量、响应与时效、满意度与评价收口在同一业务域下，按 Tab 按需加载。

import { SegmentedControl } from "@mantine/core"
import { useNavigate } from "@tanstack/react-router"
import { type PeriodRoute, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { ConversationsPage } from "./ConversationsPage"
import { ResponseTimePage } from "./ResponseTimePage"
import { SatisfactionPage } from "./SatisfactionPage"
import type { ServiceReportSearchParams } from "./search"

type ServiceTab = "volume" | "response" | "satisfaction"

/** 客服报表页面组件，整合会话量、响应时效与满意度三个切面。 */
export function ServicePage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const search = route.useSearch() as ServiceReportSearchParams
  const tab: ServiceTab = search.tab ?? "volume"

  const setTab = (next: string) => {
    void navigate({
      to: ".",
      search: ((prev: Record<string, unknown>) => {
        const out: Record<string, unknown> = { ...prev }
        if (next === "volume") delete out.tab
        else out.tab = next
        delete out.page
        return out
      }) as never,
      replace: true,
    })
  }

  const switcher = (
    <SegmentedControl
      value={tab}
      onChange={setTab}
      data={[
        { value: "volume", label: t("reports.service_tab_volume") },
        { value: "response", label: t("reports.service_tab_response") },
        { value: "satisfaction", label: t("reports.service_tab_satisfaction") },
      ]}
    />
  )

  const title = t("nav.reports_service")
  if (tab === "response") {
    return <ResponseTimePage route={route} audience="customer" title={title} toolbarRight={switcher} />
  }
  if (tab === "satisfaction") {
    return <SatisfactionPage route={route} audience="customer" title={title} toolbarRight={switcher} />
  }
  return <ConversationsPage route={route} audience="customer" title={title} toolbarRight={switcher} />
}
