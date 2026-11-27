// 合作方服务报表：面向合作方 Telegram 机器人及合作群（AI Agent + 人工协作）的请求量与响应时效分析。

import { SegmentedControl } from "@mantine/core"
import { useNavigate } from "@tanstack/react-router"
import { type PeriodRoute, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { ConversationsPage } from "./ConversationsPage"
import { ResponseTimePage } from "./ResponseTimePage"
import type { PartnerServiceReportSearchParams } from "./search"

type PartnerTab = "volume" | "response"

/** 合作方服务报表页面组件，整合合作群/机器人请求量与响应时效两个切面。 */
export function PartnerServicePage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const search = route.useSearch() as PartnerServiceReportSearchParams
  const tab: PartnerTab = search.tab ?? "volume"

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
        { value: "volume", label: t("reports.partner_tab_volume") },
        { value: "response", label: t("reports.service_tab_response") },
      ]}
    />
  )

  const title = t("nav.reports_partner")
  if (tab === "response") {
    return <ResponseTimePage route={route} audience="partner" title={title} toolbarRight={switcher} />
  }
  return <ConversationsPage route={route} audience="partner" title={title} toolbarRight={switcher} />
}
