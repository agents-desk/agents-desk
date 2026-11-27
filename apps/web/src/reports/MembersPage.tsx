// 成员报表：团队汇总指标带与工作量对比融合面板，以及全列排序绩效表（当前待处理数可一键下钻收件箱）。

import type { MemberStatus, ReportMemberMetrics } from "@agents-desk/client/tenant"
import { BarChart } from "@mantine/charts"
import { Avatar, Badge, Group, Skeleton, Stack, Text, Tooltip } from "@mantine/core"
import { IconArrowUpRight } from "@tabler/icons-react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { Link, useParams } from "@tanstack/react-router"
import {
  type Column,
  DataTable,
  LoadingStage,
  type PeriodRoute,
  useCan,
  useFormatDuration,
  useLanguage,
  useSmoothTransition,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useMemo, useState } from "react"
import { kit } from "../api"
import { StatusBadge } from "../status"
import { REPORT_BAR_PROPS } from "./Breakdown"
import { memberOpenInboxSearch } from "./drilldown"
import { csatColor, formatInt, formatPercent, formatScore } from "./format"
import { type MetricItem, MetricStrip } from "./MetricStrip"
import { ReportShell } from "./ReportShell"
import { useReportFilters } from "./search"
import { DetailSection } from "./SectionCard"

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
  const can = useCan()
  const uiLang = useLanguage()
  const formatDuration = useFormatDuration()
  const { slug } = useParams({ from: "/t/$slug" })
  const filters = useReportFilters(route)
  const query = useQuery({
    ...kit.queries.report.members(filters.queryParams),
    placeholderData: keepPreviousData,
  })
  const scopeKey = `${filters.queryParams.from}|${filters.queryParams.to}|${filters.queryParams.tz ?? ""}|${filters.teamId ?? ""}|${filters.channelId ?? ""}`
  const pageState = useSmoothTransition(query.data, scopeKey, query.isFetching)
  const data = pageState.data
  const loading = query.isPending && !data
  const s = data?.summary
  const canInbox = can("conversations:read")
  const scope = useMemo(
    () => ({ teamId: filters.teamId, channelId: filters.channelId }),
    [filters.channelId, filters.teamId],
  )

  const [sort, setSort] = useState<string | undefined>("-closed_count")

  const sortedItems = useMemo(() => {
    const list = [...(query.data?.items ?? [])]
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
        case "tickets_resolved":
          return compareNum(a.tickets_resolved, b.tickets_resolved, desc)
        case "tickets_open":
          return compareNum(a.tickets_open, b.tickets_open, desc)
        case "business_messages":
          return compareNum(a.business_messages, b.business_messages, desc)
        default:
          return 0
      }
    })
    return list
  }, [query.data?.items, sort])

  const tableState = useSmoothTransition(sortedItems, `${scopeKey}|${sort ?? ""}`, query.isFetching, { minMs: 200 })

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

  const metrics: MetricItem[] = [
    {
      key: "active_members",
      label: t("reports.member_active"),
      value: formatInt(s?.active_members, uiLang),
      secondary: data ? t("reports.member_active_sub", { total: formatInt(data.items.length, uiLang) }) : undefined,
    },
    {
      key: "closed_count",
      label: t("reports.closed_rounds"),
      value: formatInt(s?.closed_count, uiLang),
      secondary: s ? t("reports.member_replies_sub", { n: formatInt(s.replies_sent, uiLang) }) : undefined,
    },
    {
      key: "tickets_resolved",
      label: t("reports.ticket_resolved"),
      value: formatInt(s?.tickets_resolved, uiLang),
      secondary: s ? t("reports.member_biz_sub", { n: formatInt(s.business_messages, uiLang) }) : undefined,
    },
    {
      key: "team_frt",
      label: t("reports.member_team_frt"),
      value: formatDuration(s?.first_reply_median),
      secondary: s?.csat !== undefined ? t("reports.member_team_csat", { v: formatPercent(s.csat) }) : undefined,
    },
  ]

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
        width: 116,
        render: (row) =>
          canInbox && row.open_assigned > 0 ? (
            <Tooltip label={t("reports.drill_member_inbox")} openDelay={250} withArrow>
              <Link
                to="/t/$slug/inbox"
                params={{ slug }}
                search={memberOpenInboxSearch(row.member_id, scope)}
                className="rp-drill-link"
              >
                <Text span size="sm" fw={500}>
                  {formatInt(row.open_assigned, uiLang)}
                </Text>
                <IconArrowUpRight size={14} stroke={1.6} />
              </Link>
            </Tooltip>
          ) : (
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
      {
        accessor: "tickets_resolved",
        title: t("reports.col_tickets_resolved"),
        sortable: true,
        textAlign: "right",
        width: 112,
        render: (row) => (
          <Stack gap={0} align="flex-end">
            <Text size="sm" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
              {formatInt(row.tickets_resolved, uiLang)}
            </Text>
            {row.tickets_open > 0 && (
              <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                {t("reports.member_tickets_open_sub", { n: formatInt(row.tickets_open, uiLang) })}
              </Text>
            )}
          </Stack>
        ),
      },
      {
        accessor: "business_messages",
        title: t("reports.col_biz_messages"),
        sortable: true,
        textAlign: "right",
        width: 112,
        render: (row) => (
          <Stack gap={0} align="flex-end">
            <Text size="sm" style={{ fontVariantNumeric: "tabular-nums" }}>
              {formatInt(row.business_messages, uiLang)}
            </Text>
            {row.business_chats > 0 && (
              <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                {t("reports.member_biz_chats_sub", { n: formatInt(row.business_chats, uiLang) })}
              </Text>
            )}
          </Stack>
        ),
      },
    ],
    [canInbox, formatDuration, scope, slug, t, uiLang],
  )

  return (
    <ReportShell
      title={t("nav.reports_members")}
      period={filters.period}
      filters={filters}
      meta={data?.period}
      error={query.error}
      updating={pageState.busy}
    >
      {/* 主分析面板：团队指标带 + 成员工作量对比 */}
      <section className="rp-panel">
        <MetricStrip items={metrics} loading={loading} />
        {(loading || chartData.length > 0) && (
          <div className="rp-panel-body">
            <Text size="sm" fw={600} mb="sm">
              {t("reports.member_chart_title")}
            </Text>
            {loading ? (
              <Skeleton height={210} radius="sm" />
            ) : (
              <BarChart
                h={210}
                data={chartData}
                dataKey="name"
                series={[
                  { name: "closed_count", label: t("reports.col_closed"), color: "brand.6" },
                  { name: "replies_sent", label: t("reports.col_replies"), color: "bright-blue.6" },
                ]}
                barProps={REPORT_BAR_PROPS}
                withLegend
                gridAxis="y"
              />
            )}
          </div>
        )}
      </section>

      <DetailSection title={t("reports.member_table_title")}>
        <LoadingStage loading={tableState.busy && !pageState.busy}>
          <DataTable
            records={tableState.data}
            columns={columns}
            rowKey="member_id"
            loading={query.isPending && !data}
            list={{
              page: 1,
              perPage: Math.max(20, tableState.data.length),
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
        </LoadingStage>
      </DetailSection>
    </ReportShell>
  )
}
