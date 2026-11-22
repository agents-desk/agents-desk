// 成员报表：团队核心汇总、成员工作量对比柱状图，以及支持全列排序的成员绩效明细表（保留已移出成员的历史记录）。

import type { MemberStatus, ReportMemberMetrics } from "@agents-desk/client/tenant"
import { BarChart } from "@mantine/charts"
import { Avatar, Badge, Group, SimpleGrid, Stack, Text } from "@mantine/core"
import { IconCheck, IconClock, IconMessageReply, IconUsers } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import {
  type Column,
  DataTable,
  Page,
  type PeriodRoute,
  StatCard,
  useFormatDuration,
  useLanguage,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useMemo, useState } from "react"
import { kit } from "../api"
import { StatusBadge } from "../status"
import { ReportFilterBar } from "./FilterBar"
import { csatColor, formatInt, formatPercent, formatScore } from "./format"
import { useReportFilters } from "./search"
import { SectionCard } from "./SectionCard"

/** 比较可为空的数字，空值始终排在末尾。 */
function compareNum(a: number | undefined, b: number | undefined, desc: boolean): number {
  const aNull = a === undefined || a === null
  const bNull = b === undefined || b === null
  if (aNull && bNull) return 0
  if (aNull) return 1
  if (bNull) return -1
  return desc ? b - a : a - b
}

/** 成员报表页面组件。 */
export function MembersPage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const formatDuration = useFormatDuration()
  const filters = useReportFilters(route)
  const query = useQuery(kit.queries.report.members(filters.queryParams))
  const data = query.data
  const loading = query.isPending
  const s = data?.summary

  const [sort, setSort] = useState<string | undefined>("-closed_count")

  const sortedItems = useMemo(() => {
    const list = [...(data?.items ?? [])]
    if (!sort) return list
    const desc = sort.startsWith("-")
    const field = desc ? sort.slice(1) : sort
    list.sort((a, b) => {
      switch (field) {
        case "name":
          return desc ? b.name.localeCompare(a.name) : a.name.localeCompare(b.name)
        case "closed_count":
          return compareNum(a.closed_count, b.closed_count, desc)
        case "replies_sent":
          return compareNum(a.replies_sent, b.replies_sent, desc)
        case "open_assigned":
          return compareNum(a.open_assigned, b.open_assigned, desc)
        case "first_reply_median":
          return compareNum(a.first_reply_median, b.first_reply_median, desc)
        case "resolution_median":
          return compareNum(a.resolution_median, b.resolution_median, desc)
        case "csat":
          return compareNum(a.csat, b.csat, desc)
        default:
          return 0
      }
    })
    return list
  }, [data?.items, sort])

  const chartData = useMemo(
    () =>
      (data?.items ?? [])
        .filter((m) => m.closed_count > 0 || m.replies_sent > 0)
        .slice(0, 12)
        .map((m) => ({
          name: m.name || m.member_id.slice(0, 8),
          closed_count: m.closed_count,
          replies_sent: m.replies_sent,
        })),
    [data?.items],
  )

  const columns = useMemo<Column<ReportMemberMetrics>[]>(
    () => [
      {
        accessor: "name",
        title: t("reports.col_member"),
        sortable: true,
        render: (row) => (
          <Group gap="xs" wrap="nowrap">
            <Avatar src={row.avatar_url || undefined} size="sm" radius="xl" color="brand">
              {(row.name || "?").slice(0, 1).toUpperCase()}
            </Avatar>
            <Text size="sm" fw={500} truncate>
              {row.name || row.member_id}
            </Text>
            {row.removed ? (
              <Badge size="xs" variant="light" color="gray">
                {t("reports.member_removed")}
              </Badge>
            ) : row.online_status ? (
              <StatusBadge status={row.online_status as MemberStatus} />
            ) : null}
          </Group>
        ),
      },
      {
        accessor: "closed_count",
        title: t("reports.col_closed"),
        sortable: true,
        textAlign: "right",
        width: 110,
        render: (row) => (
          <Text size="sm" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
            {formatInt(row.closed_count, uiLang)}
          </Text>
        ),
      },
      {
        accessor: "replies_sent",
        title: t("reports.col_replies"),
        sortable: true,
        textAlign: "right",
        width: 110,
        render: (row) => (
          <Text size="sm" style={{ fontVariantNumeric: "tabular-nums" }}>
            {formatInt(row.replies_sent, uiLang)}
          </Text>
        ),
      },
      {
        accessor: "open_assigned",
        title: t("reports.col_open_assigned"),
        sortable: true,
        textAlign: "right",
        width: 110,
        render: (row) => (
          <Text size="sm" c={row.open_assigned > 0 ? undefined : "dimmed"} style={{ fontVariantNumeric: "tabular-nums" }}>
            {formatInt(row.open_assigned, uiLang)}
          </Text>
        ),
      },
      {
        accessor: "first_reply_median",
        title: t("reports.col_frt_median"),
        sortable: true,
        textAlign: "right",
        width: 140,
        render: (row) => (
          <Stack gap={0} align="flex-end">
            <Text size="sm" style={{ fontVariantNumeric: "tabular-nums" }}>
              {formatDuration(row.first_reply_median)}
            </Text>
            {row.first_reply_samples > 0 && (
              <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                {t("reports.samples_short", { n: formatInt(row.first_reply_samples, uiLang) })}
              </Text>
            )}
          </Stack>
        ),
      },
      {
        accessor: "resolution_median",
        title: t("reports.col_res_median"),
        sortable: true,
        textAlign: "right",
        width: 140,
        render: (row) => (
          <Stack gap={0} align="flex-end">
            <Text size="sm" style={{ fontVariantNumeric: "tabular-nums" }}>
              {formatDuration(row.resolution_median)}
            </Text>
            {row.resolution_samples > 0 && (
              <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                {t("reports.samples_short", { n: formatInt(row.resolution_samples, uiLang) })}
              </Text>
            )}
          </Stack>
        ),
      },
      {
        accessor: "csat",
        title: t("reports.col_csat"),
        sortable: true,
        textAlign: "right",
        width: 140,
        render: (row) =>
          row.rated_count > 0 ? (
            <Group gap={6} justify="flex-end" wrap="nowrap">
              <Badge size="sm" variant="light" color={csatColor(row.csat)}>
                {formatPercent(row.csat)}
              </Badge>
              <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                {formatScore(row.avg_score)} ({formatInt(row.rated_count, uiLang)})
              </Text>
            </Group>
          ) : (
            <Text size="sm" c="dimmed">
              —
            </Text>
          ),
      },
    ],
    [formatDuration, t, uiLang],
  )

  return (
    <Page title={t("nav.reports_members")} description={t("reports.member_desc")} breadcrumbs={false}>
      <Stack gap="md">
        <ReportFilterBar
          period={filters.period}
          filters={filters}
          showGranularity={false}
          meta={data?.period}
          error={query.error}
        />

        <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="md">
          <StatCard
            label={t("reports.member_active")}
            value={formatInt(s?.active_members, uiLang)}
            secondary={data ? t("reports.member_active_sub", { total: formatInt(data.items.length, uiLang) }) : undefined}
            icon={<IconUsers size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.closed_rounds")}
            value={formatInt(s?.closed_count, uiLang)}
            icon={<IconCheck size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.replies_sent")}
            value={formatInt(s?.replies_sent, uiLang)}
            icon={<IconMessageReply size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.member_team_frt")}
            value={formatDuration(s?.first_reply_median)}
            secondary={
              s?.csat !== undefined ? t("reports.member_team_csat", { v: formatPercent(s.csat) }) : undefined
            }
            icon={<IconClock size={16} stroke={1.6} />}
            loading={loading}
          />
        </SimpleGrid>

        {chartData.length > 0 && (
          <SectionCard title={t("reports.member_chart_title")} loading={loading} minHeight={220}>
            <BarChart
              h={220}
              data={chartData}
              dataKey="name"
              series={[
                { name: "closed_count", label: t("reports.col_closed"), color: "brand.6" },
                { name: "replies_sent", label: t("reports.col_replies"), color: "bright-blue.6" },
              ]}
              withLegend
              gridAxis="y"
            />
          </SectionCard>
        )}

        <SectionCard title={t("reports.member_table_title")}>
          <DataTable
            records={sortedItems}
            columns={columns}
            rowKey="member_id"
            loading={query.isFetching}
            list={{
              page: 1,
              perPage: Math.max(20, sortedItems.length),
              sort,
              setPage: () => {},
              setPerPage: () => {},
              setSort: (next) => {
                if (!next) setSort(undefined)
                else if (typeof next === "string") setSort(next)
                else setSort(`${next.desc ? "-" : ""}${next.field}`)
              },
            }}
            empty={t("reports.empty")}
          />
        </SectionCard>
      </Stack>
    </Page>
  )
}
