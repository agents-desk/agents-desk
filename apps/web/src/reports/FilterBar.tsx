// 报表顶部筛选栏：时间窗口选择器（PeriodPicker）、可选的团队收件箱与渠道筛选器，以及实际生效的统计时间范围提示。

import type { ReportPeriodMeta } from "@agents-desk/client/tenant"
import { Alert, Button, Group, Select, Stack, Text } from "@mantine/core"
import { IconAlertCircle } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { PeriodPicker, type PeriodState, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import type { ReportFiltersState } from "./search"

export interface ReportFilterBarProps {
  /** 时间窗口状态（传 filters 时自动取 filters.period）。 */
  period: PeriodState
  /** 支持团队/渠道筛选的报表传入 filters。 */
  filters?: ReportFiltersState
  /** 是否显示分桶粒度选择器（有趋势图的报表开启）。 */
  showGranularity?: boolean
  /** 后端返回的实际统计窗口元信息（用于显示生效的起止日期与时区）。 */
  meta?: ReportPeriodMeta
  /** 报表接口报错时展示友好错误提示。 */
  error?: unknown
}

/** 报表顶部筛选栏：统一时间窗口、粒度与维度筛选交互。 */
export function ReportFilterBar({
  period,
  filters,
  showGranularity = true,
  meta,
  error,
}: ReportFilterBarProps): ReactNode {
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
  const channelOptions = (channels.data ?? []).map((ch) => ({
    value: ch.id,
    label: ch.enabled ? ch.name : t("reports.channel_disabled", { name: ch.name }),
  }))

  const hasDimensionFilter = Boolean(filters?.teamId || filters?.channelId)

  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center" gap="sm" wrap="wrap">
        <Group gap="xs" wrap="wrap">
          <PeriodPicker period={period} showGranularity={showGranularity} />
          {filters && (
            <>
              <Select
                size="xs"
                w={160}
                clearable
                searchable
                placeholder={t("reports.all_teams")}
                value={filters.teamId ?? null}
                onChange={(v) => filters.setTeamId(v ?? undefined)}
                data={teamOptions}
              />
              <Select
                size="xs"
                w={160}
                clearable
                searchable
                placeholder={t("reports.all_channels")}
                value={filters.channelId ?? null}
                onChange={(v) => filters.setChannelId(v ?? undefined)}
                data={channelOptions}
              />
              {hasDimensionFilter && (
                <Button size="xs" variant="subtle" color="gray" onClick={filters.clearDimensions}>
                  {t("reports.clear_filters")}
                </Button>
              )}
            </>
          )}
        </Group>

        {meta && (
          <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
            {t("reports.period_range", { from: meta.from, to: meta.to, tz: meta.time_zone })}
          </Text>
        )}
      </Group>

      {Boolean(error) && (
        <Alert color="red" variant="light" icon={<IconAlertCircle size={16} />}>
          {t("reports.load_failed")}
        </Alert>
      )}
    </Stack>
  )
}
