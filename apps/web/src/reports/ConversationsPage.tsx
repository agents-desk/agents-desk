// 会话报表：核心 KPI、当前积压、趋势面积图，以及渠道、团队、标签和客户语言分布。

import { AreaChart, DonutChart } from "@mantine/charts"
import { Badge, Group, Progress, SimpleGrid, Stack, Table, Text } from "@mantine/core"
import { IconCheck, IconInbox, IconMessagePlus, IconRefresh } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { Page, type PeriodRoute, StatCard, useLanguage, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import { languageName } from "../languages"
import { ReportFilterBar } from "./FilterBar"
import { formatInt, pctDelta, REPORT_CHART_COLORS } from "./format"
import { useReportFilters } from "./search"
import { SectionCard } from "./SectionCard"

/** 会话报表页面组件。 */
export function ConversationsPage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const filters = useReportFilters(route)
  const query = useQuery(kit.queries.report.conversations(filters.queryParams))
  const data = query.data
  const loading = query.isPending
  const s = data?.summary

  const maxTag = Math.max(1, ...(data?.by_tag ?? []).map((x) => x.count))
  const langTotal = (data?.by_language ?? []).reduce((acc, x) => acc + x.count, 0)
  const donutData = (data?.by_language ?? []).map((item, idx) => ({
    name: item.language === "und" || !item.language ? t("reports.lang_unknown") : languageName(item.language, uiLang),
    value: item.count,
    color: REPORT_CHART_COLORS[idx % REPORT_CHART_COLORS.length],
  }))

  return (
    <Page title={t("nav.reports_conversations")} description={t("reports.conv_desc")} breadcrumbs={false}>
      <Stack gap="md">
        <ReportFilterBar period={filters.period} filters={filters} meta={data?.period} error={query.error} />

        <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="md">
          <StatCard
            label={t("reports.conv_created")}
            description={t("reports.conv_created_desc")}
            value={formatInt(s?.created, uiLang)}
            delta={pctDelta(s?.created, s?.prev_created)}
            secondary={s ? t("reports.conv_rounds_sub", { n: formatInt(s.rounds_opened, uiLang) }) : undefined}
            icon={<IconMessagePlus size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.conv_reopened")}
            description={t("reports.conv_reopened_desc")}
            value={formatInt(s?.reopened, uiLang)}
            delta={pctDelta(s?.reopened, s?.prev_reopened, true)}
            icon={<IconRefresh size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.closed_rounds")}
            value={formatInt(s?.closed, uiLang)}
            delta={pctDelta(s?.closed, s?.prev_closed)}
            icon={<IconCheck size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.backlog_now")}
            value={formatInt(s?.open_now, uiLang)}
            secondary={
              s
                ? t("reports.backlog_secondary", {
                    waiting: formatInt(s.waiting_now, uiLang),
                    unassigned: formatInt(s.unassigned_now, uiLang),
                    snoozed: formatInt(s.snoozed_now, uiLang),
                  })
                : undefined
            }
            icon={<IconInbox size={16} stroke={1.6} />}
            loading={loading}
          />
        </SimpleGrid>

        <SectionCard
          title={t("reports.conv_series_title")}
          loading={loading}
          empty={!data?.series.length}
          minHeight={260}
        >
          <AreaChart
            h={260}
            data={data?.series ?? []}
            dataKey="date"
            series={[
              { name: "created", label: t("reports.series_created"), color: "brand.6" },
              { name: "closed", label: t("reports.series_closed"), color: "green.6" },
              { name: "reopened", label: t("reports.series_reopened"), color: "deep-orange.6" },
            ]}
            curveType="monotone"
            withLegend
            withDots={false}
            gridAxis="xy"
          />
        </SectionCard>

        <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
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
                  <Table.Th>{t("reports.col_kind")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_created")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_closed")}</Table.Th>
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
                    <Table.Td>
                      <Badge size="xs" variant="light" color="gray">
                        {kit.label("ChannelKind", row.kind)}
                      </Badge>
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.created, uiLang)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.closed, uiLang)}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </SectionCard>

          <SectionCard
            title={t("reports.by_team")}
            loading={loading}
            empty={!data?.by_team.length}
            minHeight={180}
          >
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("reports.col_team")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_created")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_closed")}</Table.Th>
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
                      {formatInt(row.created, uiLang)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.closed, uiLang)}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </SectionCard>

          <SectionCard title={t("reports.by_tag")} loading={loading} empty={!data?.by_tag.length} minHeight={180}>
            <Stack gap="xs">
              {(data?.by_tag ?? []).map((tag) => (
                <Stack key={tag.id} gap={4}>
                  <Group justify="space-between" wrap="nowrap">
                    <Badge size="sm" variant="light" color={tag.color || "gray"}>
                      {tag.name}
                    </Badge>
                    <Text size="xs" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(tag.count, uiLang)}
                    </Text>
                  </Group>
                  <Progress value={(tag.count / maxTag) * 100} size="sm" color={tag.color || "brand"} radius="xl" />
                </Stack>
              ))}
            </Stack>
          </SectionCard>

          <SectionCard
            title={t("reports.by_language")}
            loading={loading}
            empty={!data?.by_language.length}
            minHeight={180}
          >
            <Group align="center" gap="lg" wrap="wrap">
              <DonutChart
                data={donutData}
                size={156}
                thickness={22}
                withTooltip
                tooltipDataSource="segment"
              />
              <Stack gap={6} style={{ flex: 1, minWidth: 160 }}>
                {donutData.map((item) => {
                  const share = langTotal > 0 ? Math.round((item.value / langTotal) * 1000) / 10 : 0
                  return (
                    <Group key={item.name} justify="space-between" gap="xs" wrap="nowrap">
                      <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                        <Badge size="xs" circle color={item.color.split(".")[0]} variant="filled" p={0} w={8} h={8} />
                        <Text size="sm" truncate>
                          {item.name}
                        </Text>
                      </Group>
                      <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums", flexShrink: 0 }}>
                        {formatInt(item.value, uiLang)} ({share}%)
                      </Text>
                    </Group>
                  )
                })}
              </Stack>
            </Group>
          </SectionCard>
        </SimpleGrid>
      </Stack>
    </Page>
  )
}
