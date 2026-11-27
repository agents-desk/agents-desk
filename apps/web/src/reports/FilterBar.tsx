// 报表紧凑工具条控件组：合并时间胶囊（PeriodButton）与可选的团队收件箱、渠道筛选器。

import { Button, Group, Select } from "@mantine/core"
import { IconX } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { type PeriodState, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import { PeriodButton } from "./PeriodButton"
import type { ReportFiltersState } from "./search"

export interface ReportFilterBarProps {
  /** 时间窗口状态。 */
  period: PeriodState
  /** 支持团队/渠道筛选的报表传入 filters。 */
  filters?: ReportFiltersState
  /** 时间选择器右侧的自定义维度筛选控件（用于工单、商务等专属筛选）。 */
  extra?: ReactNode
  /** 工具条右侧扩展区。 */
  right?: ReactNode
}

/** 报表工具条控件组，配合 PaneToolbar（dense 密度）使用。 */
export function ReportFilterBar({ period, filters, extra, right }: ReportFilterBarProps): ReactNode {
  const t = useT()
  const showDimensions = Boolean(filters)
  const teams = useQuery({
    ...kit.queries.team.list({ per_page: 100 }),
    enabled: showDimensions,
  })
  const channels = useQuery({
    ...kit.queries.channel.list(),
    enabled: showDimensions,
  })

  const teamOptions = (teams.data?.items ?? []).map((tm) => ({
    value: tm.id,
    label: tm.status === "disabled" ? t("reports.team_disabled", { name: tm.name }) : tm.name,
  }))
  const channelOptions = (channels.data ?? [])
    .filter((ch) => ch.kind !== "business_link" && ch.kind !== "telegram_account")
    .map((ch) => ({
      value: ch.id,
      label: ch.enabled ? ch.name : t("reports.channel_disabled", { name: ch.name }),
    }))

  const hasDimensionFilter = Boolean(filters?.teamId || filters?.channelId)

  return (
    <Group justify="space-between" align="center" gap="xs" wrap="wrap">
      <Group gap="xs" wrap="wrap">
        <PeriodButton period={period} />
        {extra}
        {filters && (
          <>
            <Select
              size="sm"
              w={164}
              clearable
              searchable
              placeholder={t("reports.all_teams")}
              value={filters.teamId ?? null}
              onChange={(v) => filters.setTeamId(v ?? undefined)}
              data={teamOptions}
            />
            <Select
              size="sm"
              w={164}
              clearable
              searchable
              placeholder={t("reports.all_channels")}
              value={filters.channelId ?? null}
              onChange={(v) => filters.setChannelId(v ?? undefined)}
              data={channelOptions}
            />
            {hasDimensionFilter && (
              <Button
                size="sm"
                variant="subtle"
                color="gray"
                leftSection={<IconX size={14} stroke={1.6} />}
                onClick={filters.clearDimensions}
              >
                {t("reports.clear_filters")}
              </Button>
            )}
          </>
        )}
      </Group>
      {right}
    </Group>
  )
}
