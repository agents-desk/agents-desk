// 翻译报表：调用量、成功率、Token 消耗、费用与耗时、趋势切换、按场景/模型拆解以及识别与回复语言分布。

import { AreaChart } from "@mantine/charts"
import { Badge, Group, Progress, SegmentedControl, SimpleGrid, Stack, Table, Text } from "@mantine/core"
import { IconCheck, IconCoins, IconCpu, IconLanguage } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { Page, type PeriodRoute, StatCard, useFormatDuration, useLanguage, usePeriod, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit } from "../api"
import { languageName } from "../languages"
import { ReportFilterBar } from "./FilterBar"
import { formatInt, formatPercent, formatUSD, pctDelta } from "./format"
import { SectionCard } from "./SectionCard"

type TrendMetric = "calls" | "tokens" | "cost"

/** 翻译报表页面组件。 */
export function TranslationPage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const formatDuration = useFormatDuration()
  const period = usePeriod(route, { defaultPreset: "7d" })
  const query = useQuery(kit.queries.report.translation(period.params))
  const data = query.data
  const loading = query.isPending
  const s = data?.summary

  const [metric, setMetric] = useState<TrendMetric>("calls")

  const operationLabel = (op: string): string => {
    switch (op) {
      case "incoming":
        return t("reports.op_incoming")
      case "fill":
        return t("reports.op_fill")
      case "reply":
        return t("reports.op_reply")
      case "back_translate":
        return t("reports.op_back_translate")
      default:
        return t("reports.op_other")
    }
  }

  const maxDetected = Math.max(1, ...(data?.detected_languages ?? []).map((x) => x.count))
  const maxReply = Math.max(1, ...(data?.reply_languages ?? []).map((x) => x.count))

  const seriesConfig =
    metric === "calls"
      ? [
          { name: "calls", label: t("reports.metric_calls"), color: "brand.6" },
          { name: "failed_calls", label: t("reports.series_failed"), color: "red.6" },
        ]
      : metric === "tokens"
        ? [{ name: "total_tokens", label: t("reports.metric_tokens"), color: "bright-blue.6" }]
        : [{ name: "cost_usd", label: t("reports.metric_cost"), color: "green.6" }]

  return (
    <Page title={t("nav.reports_translation")} description={t("reports.translation_desc")} breadcrumbs={false}>
      <Stack gap="md">
        <ReportFilterBar period={period} meta={data?.period} error={query.error} />

        <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="md">
          <StatCard
            label={t("reports.translation_calls")}
            value={formatInt(s?.calls, uiLang)}
            delta={pctDelta(s?.calls, s?.prev_calls)}
            secondary={
              s
                ? t("reports.translation_calls_sub", {
                    ok: formatInt(s.success_calls, uiLang),
                    failed: formatInt(s.failed_calls, uiLang),
                  })
                : undefined
            }
            icon={<IconLanguage size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.success_rate")}
            value={formatPercent(s?.success_rate)}
            secondary={s ? t("reports.success_rate_sub", { failed: formatInt(s.failed_calls, uiLang) }) : undefined}
            icon={<IconCheck size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.tokens_total")}
            value={formatInt(s?.total_tokens, uiLang)}
            delta={pctDelta(s?.total_tokens, s?.prev_total_tokens)}
            secondary={
              s
                ? t("reports.tokens_sub", {
                    prompt: formatInt(s.prompt_tokens, uiLang),
                    completion: formatInt(s.completion_tokens, uiLang),
                  })
                : undefined
            }
            icon={<IconCpu size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.cost_and_latency")}
            value={formatUSD(s?.total_cost_usd)}
            delta={pctDelta(s?.total_cost_usd, s?.prev_total_cost_usd, true)}
            secondary={
              s
                ? t("reports.cost_sub", {
                    ms: formatDuration(s.avg_duration_ms, { unit: "ms" }),
                    n: formatInt(s.costed_calls, uiLang),
                  })
                : undefined
            }
            icon={<IconCoins size={16} stroke={1.6} />}
            loading={loading}
          />
        </SimpleGrid>

        <SectionCard
          title={t("reports.translation_series")}
          action={
            <SegmentedControl
              size="xs"
              value={metric}
              onChange={(v) => setMetric(v as TrendMetric)}
              data={[
                { value: "calls", label: t("reports.metric_calls") },
                { value: "tokens", label: t("reports.metric_tokens") },
                { value: "cost", label: t("reports.metric_cost") },
              ]}
            />
          }
          loading={loading}
          empty={!data?.series.length}
          minHeight={260}
        >
          <AreaChart
            h={260}
            data={data?.series ?? []}
            dataKey="date"
            series={seriesConfig}
            curveType="monotone"
            withLegend
            withDots={false}
            gridAxis="xy"
            valueFormatter={(v) => (metric === "cost" ? formatUSD(v) : formatInt(v, uiLang))}
          />
        </SectionCard>

        <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
          <SectionCard
            title={t("reports.by_operation")}
            loading={loading}
            empty={!data?.by_operation.length}
            minHeight={180}
          >
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("reports.col_operation")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_calls")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_success_rate")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_avg_duration")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_cost")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(data?.by_operation ?? []).map((row) => (
                  <Table.Tr key={row.operation}>
                    <Table.Td>
                      <Text size="sm" fw={500}>
                        {operationLabel(row.operation)}
                      </Text>
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.calls, uiLang)}
                    </Table.Td>
                    <Table.Td ta="right">
                      <Badge
                        size="sm"
                        variant="light"
                        color={row.failed_calls === 0 ? "green" : (row.success_rate ?? 0) >= 90 ? "yellow" : "red"}
                      >
                        {formatPercent(row.success_rate)}
                      </Badge>
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatDuration(row.avg_duration_ms, { unit: "ms" })}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatUSD(row.cost_usd)}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </SectionCard>

          <SectionCard title={t("reports.by_model")} loading={loading} empty={!data?.by_model.length} minHeight={180}>
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("reports.col_model")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_calls")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_tokens")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_avg_duration")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_cost")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(data?.by_model ?? []).map((row) => (
                  <Table.Tr key={row.model}>
                    <Table.Td>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" ff="monospace" fw={500} truncate>
                          {row.model}
                        </Text>
                        {row.failed_calls > 0 && (
                          <Badge size="xs" variant="light" color="red">
                            {t("reports.col_failed")} {formatInt(row.failed_calls, uiLang)}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.calls, uiLang)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.total_tokens, uiLang)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatDuration(row.avg_duration_ms, { unit: "ms" })}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatUSD(row.cost_usd)}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </SectionCard>

          <SectionCard
            title={t("reports.detected_languages")}
            loading={loading}
            empty={!data?.detected_languages.length}
            minHeight={160}
          >
            <Stack gap="xs">
              {(data?.detected_languages ?? []).map((item) => (
                <Stack key={item.language} gap={4}>
                  <Group justify="space-between" wrap="nowrap">
                    <Text size="sm">
                      {item.language === "und" || !item.language
                        ? t("reports.lang_unknown")
                        : languageName(item.language, uiLang)}
                    </Text>
                    <Text size="xs" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(item.count, uiLang)}
                    </Text>
                  </Group>
                  <Progress value={(item.count / maxDetected) * 100} size="sm" color="bright-blue" radius="xl" />
                </Stack>
              ))}
            </Stack>
          </SectionCard>

          <SectionCard
            title={t("reports.reply_languages")}
            loading={loading}
            empty={!data?.reply_languages.length}
            minHeight={160}
          >
            <Stack gap="xs">
              {(data?.reply_languages ?? []).map((item) => (
                <Stack key={item.language} gap={4}>
                  <Group justify="space-between" wrap="nowrap">
                    <Text size="sm">{languageName(item.language, uiLang)}</Text>
                    <Text size="xs" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(item.count, uiLang)}
                    </Text>
                  </Group>
                  <Progress value={(item.count / maxReply) * 100} size="sm" color="brand" radius="xl" />
                </Stack>
              ))}
            </Stack>
          </SectionCard>
        </SimpleGrid>
      </Stack>
    </Page>
  )
}
