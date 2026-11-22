// 响应时间报表：首次回复时长（FRT）与解决时长的分位数、中位数趋势、时长直方图及团队/渠道对比。

import { AreaChart, BarChart } from "@mantine/charts"
import { Badge, Group, SimpleGrid, Stack, Table, Text } from "@mantine/core"
import { IconClock, IconClockCheck, IconGauge, IconHourglassHigh } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { Page, type PeriodRoute, StatCard, useFormatDuration, useLanguage, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import { ReportFilterBar } from "./FilterBar"
import { formatInt, pctDelta } from "./format"
import { useReportFilters } from "./search"
import { SectionCard } from "./SectionCard"

/** 响应时间报表页面组件。 */
export function ResponseTimePage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const formatDuration = useFormatDuration()
  const filters = useReportFilters(route)
  const query = useQuery(kit.queries.report.responseTime(filters.queryParams))
  const data = query.data
  const loading = query.isPending

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

  return (
    <Page title={t("nav.reports_response_time")} description={t("reports.response_time_desc")} breadcrumbs={false}>
      <Stack gap="md">
        <ReportFilterBar period={filters.period} filters={filters} meta={data?.period} error={query.error} />

        <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="md">
          <StatCard
            label={t("reports.frt_median")}
            description={t("reports.frt_desc")}
            value={formatDuration(frt?.median_seconds)}
            delta={pctDelta(frt?.median_seconds, frt?.prev_median_seconds, true)}
            secondary={
              frt
                ? t("reports.samples_avg_p90", {
                    avg: formatDuration(frt.avg_seconds),
                    p90: formatDuration(frt.p90_seconds),
                    n: formatInt(frt.samples, uiLang),
                  })
                : undefined
            }
            icon={<IconClock size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.frt_p90")}
            value={formatDuration(frt?.p90_seconds)}
            secondary={
              frt
                ? t("reports.samples_avg_only", {
                    avg: formatDuration(frt.avg_seconds),
                    n: formatInt(frt.samples, uiLang),
                  })
                : undefined
            }
            icon={<IconGauge size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.res_median")}
            description={t("reports.res_desc")}
            value={formatDuration(res?.median_seconds)}
            delta={pctDelta(res?.median_seconds, res?.prev_median_seconds, true)}
            secondary={
              res
                ? t("reports.samples_avg_p90", {
                    avg: formatDuration(res.avg_seconds),
                    p90: formatDuration(res.p90_seconds),
                    n: formatInt(res.samples, uiLang),
                  })
                : undefined
            }
            icon={<IconClockCheck size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.res_p90")}
            value={formatDuration(res?.p90_seconds)}
            secondary={
              res
                ? t("reports.samples_avg_only", {
                    avg: formatDuration(res.avg_seconds),
                    n: formatInt(res.samples, uiLang),
                  })
                : undefined
            }
            icon={<IconHourglassHigh size={16} stroke={1.6} />}
            loading={loading}
          />
        </SimpleGrid>

        <SectionCard
          title={t("reports.response_time_series")}
          loading={loading}
          empty={!data?.series.length || (!frt?.samples && !res?.samples)}
          minHeight={260}
        >
          <AreaChart
            h={260}
            data={data?.series ?? []}
            dataKey="date"
            series={[
              { name: "first_reply_median", label: t("reports.series_frt"), color: "bright-blue.6" },
              { name: "resolution_median", label: t("reports.series_res"), color: "brand.6" },
            ]}
            curveType="monotone"
            connectNulls
            withLegend
            withDots={false}
            gridAxis="xy"
            valueFormatter={(v) => formatDuration(v)}
          />
        </SectionCard>

        <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
          <SectionCard title={t("reports.frt_buckets")} loading={loading} empty={!hasFrtBuckets} minHeight={220}>
            <BarChart
              h={220}
              data={frtBuckets}
              dataKey="bucket"
              series={[{ name: "count", label: t("reports.col_samples"), color: "bright-blue.6" }]}
              gridAxis="y"
            />
          </SectionCard>

          <SectionCard title={t("reports.res_buckets")} loading={loading} empty={!hasResBuckets} minHeight={220}>
            <BarChart
              h={220}
              data={resBuckets}
              dataKey="bucket"
              series={[{ name: "count", label: t("reports.col_samples"), color: "brand.6" }]}
              gridAxis="y"
            />
          </SectionCard>
        </SimpleGrid>

        <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
          <SectionCard title={t("reports.by_team")} loading={loading} empty={!data?.by_team.length} minHeight={180}>
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("reports.col_team")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_frt_median")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_res_median")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_samples")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(data?.by_team ?? []).map((row) => (
                  <Table.Tr key={row.id ?? "__unassigned__"}>
                    <Table.Td>
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
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatDuration(row.first_reply_median)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatDuration(row.resolution_median)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      <Text size="xs" c="dimmed">
                        {formatInt(row.first_reply_samples, uiLang)} / {formatInt(row.resolution_samples, uiLang)}
                      </Text>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </SectionCard>

          <SectionCard
            title={t("reports.by_channel")}
            loading={loading}
            empty={!data?.by_channel.length}
            minHeight={180}
          >
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("reports.col_channel")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_frt_median")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_res_median")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_samples")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(data?.by_channel ?? []).map((row) => (
                  <Table.Tr key={row.id}>
                    <Table.Td>
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
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatDuration(row.first_reply_median)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatDuration(row.resolution_median)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      <Text size="xs" c="dimmed">
                        {formatInt(row.first_reply_samples, uiLang)} / {formatInt(row.resolution_samples, uiLang)}
                      </Text>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </SectionCard>
        </SimpleGrid>
      </Stack>
    </Page>
  )
}
