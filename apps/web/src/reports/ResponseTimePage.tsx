// 响应时间报表：首响与解决时长指标带与趋势融合面板、时长分布直方图，以及支持聚焦筛选与下钻的团队/渠道对比。

import { AreaChart, BarChart } from "@mantine/charts"
import { Badge, Center, Group, SimpleGrid, Skeleton, Text } from "@mantine/core"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { useParams } from "@tanstack/react-router"
import { LoadingStage, type PeriodRoute, useCan, useFormatDuration, useLanguage, useSmoothTransition, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import { InboxDrillButton, REPORT_AREA_PROPS, REPORT_BAR_PROPS } from "./Breakdown"
import { channelInboxSearch, teamInboxSearch } from "./drilldown"
import { formatInt, pctDelta } from "./format"
import { type MetricItem, MetricStrip } from "./MetricStrip"
import { GranularityControl } from "./PeriodButton"
import { ReportShell } from "./ReportShell"
import { useReportFilters } from "./search"
import { SectionCard } from "./SectionCard"

/** 响应时间报表页面组件（同时服务对外客服、合作方服务与内部服务场景）。 */
export function ResponseTimePage({
  route,
  audience,
  title,
  toolbarRight,
}: {
  route: PeriodRoute
  audience?: "customer" | "partner" | "internal"
  title?: ReactNode
  toolbarRight?: ReactNode
}): ReactNode {
  const t = useT()
  const can = useCan()
  const uiLang = useLanguage()
  const formatDuration = useFormatDuration()
  const { slug } = useParams({ from: "/$slug" })
  const filters = useReportFilters(route, { defaultPreset: "7d", audience })
  const query = useQuery({
    ...kit.queries.report.responseTime(filters.queryParams),
    placeholderData: keepPreviousData,
  })
  const scopeKey = `${filters.queryParams.from}|${filters.queryParams.to}|${filters.queryParams.tz ?? ""}|${audience ?? ""}|${filters.teamId ?? ""}|${filters.channelId ?? ""}`
  const granKey = `${scopeKey}|${filters.queryParams.granularity ?? ""}`
  const pageState = useSmoothTransition(query.data, scopeKey, query.isFetching)
  const chartState = useSmoothTransition(query.data?.series, granKey, query.isFetching)
  const data = pageState.data
  const loading = query.isPending && !data
  const canInbox = can("conversations:read")
  const scope = { audience, teamId: filters.teamId, channelId: filters.channelId }

  const frt = data?.first_reply
  const res = data?.resolution

  const bucketLabel = (key: string): string => {
    switch (key) {
      case "lt_5m":
        return t("reports.bucket_lt_5m")
      case "5m_15m":
        return t("reports.bucket_5m_15m")
      case "15m_1h":
        return t("reports.bucket_15m_1h")
      case "1h_4h":
        return t("reports.bucket_1h_4h")
      case "4h_24h":
        return t("reports.bucket_4h_24h")
      default:
        return t("reports.bucket_gt_24h")
    }
  }

  const frtBuckets = (data?.first_reply_buckets ?? []).map((b) => ({
    bucket: bucketLabel(b.key),
    count: b.count,
  }))
  const resBuckets = (data?.resolution_buckets ?? []).map((b) => ({
    bucket: bucketLabel(b.key),
    count: b.count,
  }))
  const hasFrtBuckets = frtBuckets.some((b) => b.count > 0)
  const hasResBuckets = resBuckets.some((b) => b.count > 0)

  const metrics: MetricItem[] = [
    {
      key: "frt_median",
      label: t("reports.frt_median"),
      description: t("reports.frt_desc"),
      value: formatDuration(frt?.median_seconds),
      delta: pctDelta(frt?.median_seconds, frt?.prev_median_seconds, true),
      secondary: frt
        ? t("reports.samples_avg_p90", {
            avg: formatDuration(frt.avg_seconds),
            p90: formatDuration(frt.p90_seconds),
            n: formatInt(frt.samples, uiLang),
          })
        : undefined,
    },
    {
      key: "frt_p90",
      label: t("reports.frt_p90"),
      value: formatDuration(frt?.p90_seconds),
      secondary: frt
        ? t("reports.samples_avg_only", {
            avg: formatDuration(frt.avg_seconds),
            n: formatInt(frt.samples, uiLang),
          })
        : undefined,
    },
    {
      key: "res_median",
      label: t("reports.res_median"),
      description: t("reports.res_desc"),
      value: formatDuration(res?.median_seconds),
      delta: pctDelta(res?.median_seconds, res?.prev_median_seconds, true),
      secondary: res
        ? t("reports.samples_avg_p90", {
            avg: formatDuration(res.avg_seconds),
            p90: formatDuration(res.p90_seconds),
            n: formatInt(res.samples, uiLang),
          })
        : undefined,
    },
    {
      key: "res_p90",
      label: t("reports.res_p90"),
      value: formatDuration(res?.p90_seconds),
      secondary: res
        ? t("reports.samples_avg_only", {
            avg: formatDuration(res.avg_seconds),
            n: formatInt(res.samples, uiLang),
          })
        : undefined,
    },
  ]

  const toggleTeam = (id: string | undefined) => {
    if (!id) return
    filters.setTeamId(filters.teamId === id ? undefined : id)
  }

  const toggleChannel = (id: string) => {
    filters.setChannelId(filters.channelId === id ? undefined : id)
  }

  const seriesEmpty = !chartState.data?.length || (!frt?.samples && !res?.samples)

  return (
    <ReportShell
      title={title ?? t("nav.reports_service")}
      period={filters.period}
      filters={filters}
      meta={data?.period}
      error={query.error}
      updating={pageState.busy}
      toolbarRight={toolbarRight}
    >
      {/* 主分析面板：指标带 + 中位数趋势图 */}
      <section className="rp-panel">
        <MetricStrip items={metrics} loading={loading} />
        <div className="rp-panel-body">
          <Group justify="space-between" align="center" gap="xs" mb="sm" wrap="wrap">
            <Text size="sm" fw={600}>
              {t("reports.response_time_series")}
            </Text>
            <GranularityControl period={filters.period} />
          </Group>
          <LoadingStage loading={chartState.busy && !pageState.busy}>
            {loading ? (
              <Skeleton height={240} radius="sm" />
            ) : seriesEmpty ? (
              <Center mih={240}>
                <Text size="sm" c="dimmed">
                  {t("reports.empty")}
                </Text>
              </Center>
            ) : (
              <AreaChart
                h={240}
                data={chartState.data ?? []}
                dataKey="date"
                series={[
                  { name: "first_reply_median", label: t("reports.series_frt"), color: "bright-blue.6" },
                  { name: "resolution_median", label: t("reports.series_res"), color: "brand.6" },
                ]}
                curveType="monotone"
                strokeWidth={2}
                fillOpacity={0.14}
                areaProps={REPORT_AREA_PROPS}
                connectNulls
                withLegend
                withDots={false}
                gridAxis="y"
                valueFormatter={(v) => formatDuration(v)}
              />
            )}
          </LoadingStage>
        </div>
      </section>

      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
        <SectionCard title={t("reports.frt_buckets")} loading={loading} empty={!hasFrtBuckets} minHeight={210}>
          <BarChart
            h={210}
            data={frtBuckets}
            dataKey="bucket"
            series={[{ name: "count", label: t("reports.col_samples"), color: "bright-blue.6" }]}
            barProps={REPORT_BAR_PROPS}
            gridAxis="y"
          />
        </SectionCard>

        <SectionCard title={t("reports.res_buckets")} loading={loading} empty={!hasResBuckets} minHeight={210}>
          <BarChart
            h={210}
            data={resBuckets}
            dataKey="bucket"
            series={[{ name: "count", label: t("reports.col_samples"), color: "brand.6" }]}
            barProps={REPORT_BAR_PROPS}
            gridAxis="y"
          />
        </SectionCard>
      </SimpleGrid>

      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
        <SectionCard
          title={t("reports.by_team")}
          action={
            <Text size="xs" c="dimmed">
              {filters.teamId ? t("reports.focus_clear_hint") : t("reports.focus_hint")}
            </Text>
          }
          loading={loading}
          empty={!data?.by_team.length}
          minHeight={160}
          flush
        >
          <table className="rp-table">
            <thead>
              <tr>
                <th>{t("reports.col_team")}</th>
                <th data-align="right">{t("reports.col_frt_median")}</th>
                <th data-align="right">{t("reports.col_res_median")}</th>
                <th data-align="right">{t("reports.col_samples")}</th>
                {canInbox && <th style={{ width: 36 }} />}
              </tr>
            </thead>
            <tbody>
              {(data?.by_team ?? []).map((row) => {
                const clickable = Boolean(row.id)
                const selected = Boolean(row.id && filters.teamId === row.id)
                return (
                  <tr
                    key={row.id ?? "__unassigned__"}
                    className="rp-row"
                    data-clickable={clickable ? "" : undefined}
                    data-selected={selected || undefined}
                    onClick={clickable ? () => toggleTeam(row.id) : undefined}
                  >
                    <td>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" fw={500} c={row.id ? undefined : "dimmed"} truncate>
                          {row.id ? row.name : t("reports.no_team")}
                        </Text>
                        {row.disabled && (
                          <Badge size="xs" variant="light" color="gray">
                            {t("reports.disabled_badge")}
                          </Badge>
                        )}
                      </Group>
                    </td>
                    <td data-align="right">{formatDuration(row.first_reply_median)}</td>
                    <td data-align="right">{formatDuration(row.resolution_median)}</td>
                    <td data-align="right">
                      <Text span size="xs" c="dimmed">
                        {formatInt(row.first_reply_samples, uiLang)} / {formatInt(row.resolution_samples, uiLang)}
                      </Text>
                    </td>
                    {canInbox && (
                      <td data-align="right">
                        <InboxDrillButton
                          slug={slug}
                          search={teamInboxSearch(row.id, scope)}
                          label={t("reports.drill_team_inbox")}
                        />
                      </td>
                    )}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </SectionCard>

        <SectionCard
          title={t("reports.by_channel")}
          action={
            <Text size="xs" c="dimmed">
              {filters.channelId ? t("reports.focus_clear_hint") : t("reports.focus_hint")}
            </Text>
          }
          loading={loading}
          empty={!data?.by_channel.length}
          minHeight={160}
          flush
        >
          <table className="rp-table">
            <thead>
              <tr>
                <th>{t("reports.col_channel")}</th>
                <th data-align="right">{t("reports.col_frt_median")}</th>
                <th data-align="right">{t("reports.col_res_median")}</th>
                <th data-align="right">{t("reports.col_samples")}</th>
                {canInbox && <th style={{ width: 36 }} />}
              </tr>
            </thead>
            <tbody>
              {(data?.by_channel ?? []).map((row) => {
                const selected = filters.channelId === row.id
                return (
                  <tr
                    key={row.id}
                    className="rp-row"
                    data-clickable=""
                    data-selected={selected || undefined}
                    onClick={() => toggleChannel(row.id)}
                  >
                    <td>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" fw={500} truncate>
                          {row.name}
                        </Text>
                        {!row.enabled && (
                          <Badge size="xs" variant="light" color="gray">
                            {t("reports.disabled_badge")}
                          </Badge>
                        )}
                      </Group>
                    </td>
                    <td data-align="right">{formatDuration(row.first_reply_median)}</td>
                    <td data-align="right">{formatDuration(row.resolution_median)}</td>
                    <td data-align="right">
                      <Text span size="xs" c="dimmed">
                        {formatInt(row.first_reply_samples, uiLang)} / {formatInt(row.resolution_samples, uiLang)}
                      </Text>
                    </td>
                    {canInbox && (
                      <td data-align="right">
                        <InboxDrillButton
                          slug={slug}
                          search={channelInboxSearch(row.id, scope)}
                          label={t("reports.drill_channel_inbox")}
                        />
                      </td>
                    )}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </SectionCard>
      </SimpleGrid>
    </ReportShell>
  )
}
