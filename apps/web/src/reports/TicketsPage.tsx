// 工单报表：顶部工单实时积压条、周期新建与解决趋势、解决效率及按类型/团队/优先级/可见范围/负责人拆解。

import { AreaChart } from "@mantine/charts"
import { Avatar, Badge, Button, Group, Progress, Select, SimpleGrid, Skeleton, Stack, Text, Tooltip } from "@mantine/core"
import { IconArrowUpRight, IconX } from "@tabler/icons-react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { Link, useNavigate, useParams } from "@tanstack/react-router"
import {
  LoadingStage,
  type MetricItem,
  MetricStrip,
  type PeriodRoute,
  useCan,
  useFormatDuration,
  useLanguage,
  usePeriod,
  useSmoothTransition,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useCallback } from "react"
import { kit } from "../api"
import { InlineShareBar, REPORT_AREA_PROPS } from "./Breakdown"
import { formatInt, pctDelta } from "./format"
import { GranularityControl } from "./PeriodButton"
import { ReportShell } from "./ReportShell"
import type { TicketsReportSearchParams } from "./search"
import { ReportEmpty, SectionCard } from "./SectionCard"

/** 工单报表页面组件。 */
export function TicketsPage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const can = useCan()
  const uiLang = useLanguage()
  const formatDuration = useFormatDuration()
  const { slug } = useParams({ from: "/$slug" })
  const navigate = useNavigate()
  const period = usePeriod(route, { defaultPreset: "7d" })
  const search = route.useSearch() as TicketsReportSearchParams
  const teamId = search.team_id
  const typeId = search.type_id
  const visibility = search.visibility

  const patchSearch = useCallback(
    (patch: Record<string, unknown>) => {
      void navigate({
        to: ".",
        search: ((prev: Record<string, unknown>) => {
          const next: Record<string, unknown> = { ...prev }
          for (const [k, v] of Object.entries(patch)) {
            if (v === undefined || v === "") delete next[k]
            else next[k] = v
          }
          return next
        }) as never,
        replace: true,
      })
    },
    [navigate],
  )

  const teams = useQuery(kit.queries.team.list({ per_page: 100 }))
  const ticketTypes = useQuery(kit.queries.ticket.typeList({}))

  const queryParams = {
    ...period.params,
    ...(teamId ? { team_id: teamId } : {}),
    ...(typeId ? { type_id: typeId } : {}),
    ...(visibility ? { visibility } : {}),
  }
  const query = useQuery({
    ...kit.queries.report.tickets(queryParams),
    placeholderData: keepPreviousData,
  })

  const scopeKey = `${queryParams.from}|${queryParams.to}|${queryParams.tz ?? ""}|${teamId ?? ""}|${typeId ?? ""}|${visibility ?? ""}`
  const granKey = `${scopeKey}|${queryParams.granularity ?? ""}`
  const pageState = useSmoothTransition(query.data, scopeKey, query.isFetching)
  const chartState = useSmoothTransition(query.data?.series, granKey, query.isFetching)
  const data = pageState.data
  const loading = query.isPending && !data
  const s = data?.summary
  const canTickets = can("conversations:read")

  const hasFilters = Boolean(teamId || typeId || visibility)

  const toolbarExtra = (
    <>
      <Select
        size="sm"
        w={164}
        clearable
        searchable
        placeholder={t("reports.all_teams")}
        value={teamId ?? null}
        onChange={(v) => patchSearch({ team_id: v ?? undefined })}
        data={(teams.data?.items ?? []).map((tm) => ({
          value: tm.id,
          label: tm.status === "disabled" ? t("reports.team_disabled", { name: tm.name }) : tm.name,
        }))}
      />
      <Select
        size="sm"
        w={164}
        clearable
        searchable
        placeholder={t("reports.all_ticket_types")}
        value={typeId ?? null}
        onChange={(v) => patchSearch({ type_id: v ?? undefined })}
        data={(ticketTypes.data ?? []).map((tt) => ({
          value: tt.id,
          label: tt.status === "disabled" ? t("reports.channel_disabled", { name: tt.name }) : tt.name,
        }))}
      />
      <Select
        size="sm"
        w={140}
        clearable
        placeholder={t("reports.all_visibility")}
        value={visibility ?? null}
        onChange={(v) => patchSearch({ visibility: v ?? undefined })}
        data={[
          { value: "all", label: t("reports.vis_all") },
          { value: "members", label: t("reports.vis_members") },
        ]}
      />
      {hasFilters && (
        <Button
          size="sm"
          variant="subtle"
          color="gray"
          leftSection={<IconX size={14} stroke={1.6} />}
          onClick={() => patchSearch({ team_id: undefined, type_id: undefined, visibility: undefined })}
        >
          {t("reports.clear_filters")}
        </Button>
      )}
    </>
  )

  const liveItems = [
    {
      key: "submitted",
      label: t("reports.ticket_live_submitted"),
      value: s?.submitted_now,
      search: { box: "all", status: "submitted" },
    },
    {
      key: "in_progress",
      label: t("reports.ticket_live_in_progress"),
      value: s?.in_progress_now,
      search: { box: "all", status: "in_progress" },
    },
    {
      key: "waiting",
      label: t("reports.ticket_live_waiting"),
      value: s?.waiting_now,
      search: { box: "all", status: "waiting_on_customer" },
    },
    {
      key: "unassigned",
      label: t("reports.ticket_live_unassigned"),
      value: s?.unassigned_now,
      warn: Boolean(s && s.unassigned_now > 0),
      search: { box: "unassigned", status: "unresolved" },
    },
  ]

  const metrics: MetricItem[] = [
    {
      key: "created",
      label: t("reports.ticket_created"),
      value: formatInt(s?.created, uiLang),
      delta: pctDelta(s?.created, s?.prev_created),
    },
    {
      key: "resolved",
      label: t("reports.ticket_resolved"),
      value: formatInt(s?.resolved, uiLang),
      delta: pctDelta(s?.resolved, s?.prev_resolved),
    },
    {
      key: "res_median",
      label: t("reports.ticket_res_median"),
      value: formatDuration(s?.resolution_median_seconds),
      delta: pctDelta(s?.resolution_median_seconds, s?.prev_resolution_median_seconds, true),
      secondary: s?.resolution_avg_seconds !== undefined
        ? t("reports.ticket_res_avg", { avg: formatDuration(s.resolution_avg_seconds) })
        : undefined,
    },
    {
      key: "linked_contacts",
      label: t("reports.ticket_linked_contacts"),
      description: t("reports.ticket_linked_contacts_desc"),
      value: formatInt(s?.linked_contacts, uiLang),
    },
  ]

  const maxType = Math.max(1, ...(data?.by_type ?? []).map((x) => x.created))
  const maxTeam = Math.max(1, ...(data?.by_team ?? []).map((x) => x.created))
  const maxAssignee = Math.max(1, ...(data?.by_assignee ?? []).map((x) => x.resolved))

  return (
    <ReportShell
      title={t("nav.reports_tickets")}
      period={period}
      toolbarExtra={toolbarExtra}
      meta={data?.period}
      error={query.error}
      updating={pageState.busy}
      exportConfig={{ report: "tickets", params: queryParams }}
    >
      {/* 实时积压状态条 */}
      <div className="rp-live-strip">
        <span className="rp-live-badge">
          <span className="rp-live-dot" aria-hidden="true" />
          {t("reports.live_label")}
        </span>
        <div className="rp-live-items">
          {liveItems.map((item) => {
            const inner = (
              <>
                <Text span size="xs" c="dimmed">
                  {item.label}
                </Text>
                {loading ? (
                  <Skeleton height={16} width={28} radius="xs" />
                ) : (
                  <Text span className="rp-live-value" c={item.warn ? "deep-orange" : undefined}>
                    {formatInt(item.value, uiLang)}
                  </Text>
                )}
                {canTickets && <IconArrowUpRight size={14} stroke={1.6} className="rp-live-arrow" />}
              </>
            )
            if (!canTickets) {
              return (
                <span key={item.key} className="rp-live-item">
                  {inner}
                </span>
              )
            }
            return (
              <Tooltip key={item.key} label={t("reports.drill_tickets")} openDelay={250}>
                <Link
                  to="/$slug/inbox/tickets"
                  params={{ slug }}
                  search={item.search as never}
                  className="rp-live-item"
                  data-clickable=""
                >
                  {inner}
                </Link>
              </Tooltip>
            )
          })}
        </div>
      </div>

      {/* 主分析面板：指标带 + 工单趋势图 */}
      <section className="rp-panel">
        <MetricStrip items={metrics} loading={loading} />
        <div className="rp-panel-body">
          <Group justify="space-between" align="center" gap="xs" mb="sm" wrap="wrap">
            <Text size="sm" fw={600}>
              {t("reports.ticket_series_title")}
            </Text>
            <GranularityControl period={period} />
          </Group>
          <LoadingStage loading={chartState.busy && !pageState.busy}>
            {loading ? (
              <Skeleton height={240} radius="sm" />
            ) : !chartState.data?.length ? (
              <ReportEmpty minHeight={240} />
            ) : (
              <AreaChart
                h={240}
                data={chartState.data}
                dataKey="date"
                series={[
                  { name: "created", label: t("reports.ticket_created"), color: "brand.6" },
                  { name: "resolved", label: t("reports.ticket_resolved"), color: "green.6" },
                ]}
                curveType="monotone"
                strokeWidth={2}
                fillOpacity={0.14}
                areaProps={REPORT_AREA_PROPS}
                withLegend
                withDots={false}
                gridAxis="y"
                valueFormatter={(v) => formatInt(v, uiLang)}
              />
            )}
          </LoadingStage>
        </div>
      </section>

      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
        <SectionCard
          title={t("reports.ticket_by_type")}
          loading={loading}
          empty={!data?.by_type.length}
          minHeight={180}
          flush
        >
          <div className="rp-table-scroll">
            <table className="rp-table">
              <thead>
                <tr>
                  <th>{t("reports.col_ticket_type")}</th>
                  <th data-align="right">{t("reports.ticket_created")}</th>
                  <th data-align="right">{t("reports.ticket_resolved")}</th>
                  <th data-align="right">{t("reports.col_open_now")}</th>
                </tr>
              </thead>
              <tbody>
                {(data?.by_type ?? []).map((row) => {
                  const clickable = Boolean(row.id)
                  const selected = Boolean(row.id && typeId === row.id)
                  return (
                    <tr
                      key={row.id ?? "__general__"}
                      className="rp-row"
                      data-clickable={clickable ? "" : undefined}
                      data-selected={selected ? "" : undefined}
                      tabIndex={clickable ? 0 : undefined}
                      aria-selected={clickable ? selected : undefined}
                      onClick={clickable ? () => patchSearch({ type_id: typeId === row.id ? undefined : row.id }) : undefined}
                      onKeyDown={
                        clickable
                          ? (e) => {
                              if (e.target !== e.currentTarget) return
                              if (e.key === "Enter" || e.key === " ") {
                                e.preventDefault()
                                patchSearch({ type_id: typeId === row.id ? undefined : row.id })
                              }
                            }
                          : undefined
                      }
                    >
                      <td>
                        <Group gap={6} wrap="nowrap">
                          <Text size="sm" fw={500} c={row.id ? undefined : "dimmed"}>
                            {row.id ? row.name : t("reports.ticket_type_general")}
                          </Text>
                          {row.disabled && (
                            <Badge size="xs" variant="light" color="gray">
                              {t("common.disabled")}
                            </Badge>
                          )}
                        </Group>
                      </td>
                      <td data-align="right">
                        <div>{formatInt(row.created, uiLang)}</div>
                        <InlineShareBar ratio={(row.created / maxType) * 100} />
                      </td>
                      <td data-align="right">{formatInt(row.resolved, uiLang)}</td>
                      <td data-align="right">{formatInt(row.open_now, uiLang)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </SectionCard>

        <SectionCard
          title={t("reports.by_team")}
          loading={loading}
          empty={!data?.by_team.length}
          minHeight={180}
          flush
        >
          <div className="rp-table-scroll">
            <table className="rp-table">
              <thead>
                <tr>
                  <th>{t("reports.col_team")}</th>
                  <th data-align="right">{t("reports.ticket_created")}</th>
                  <th data-align="right">{t("reports.ticket_resolved")}</th>
                </tr>
              </thead>
              <tbody>
                {(data?.by_team ?? []).map((row) => {
                  const clickable = Boolean(row.id)
                  const selected = Boolean(row.id && teamId === row.id)
                  return (
                    <tr
                      key={row.id ?? "__none__"}
                      className="rp-row"
                      data-clickable={clickable ? "" : undefined}
                      data-selected={selected ? "" : undefined}
                      tabIndex={clickable ? 0 : undefined}
                      aria-selected={clickable ? selected : undefined}
                      onClick={clickable ? () => patchSearch({ team_id: teamId === row.id ? undefined : row.id }) : undefined}
                      onKeyDown={
                        clickable
                          ? (e) => {
                              if (e.target !== e.currentTarget) return
                              if (e.key === "Enter" || e.key === " ") {
                                e.preventDefault()
                                patchSearch({ team_id: teamId === row.id ? undefined : row.id })
                              }
                            }
                          : undefined
                      }
                    >
                      <td>
                        <Group gap={6} wrap="nowrap">
                          <Text size="sm" fw={500} c={row.id ? undefined : "dimmed"}>
                            {row.id ? row.name : t("reports.team_unassigned")}
                          </Text>
                          {row.disabled && (
                            <Badge size="xs" variant="light" color="gray">
                              {t("common.disabled")}
                            </Badge>
                          )}
                        </Group>
                      </td>
                      <td data-align="right">
                        <div>{formatInt(row.created, uiLang)}</div>
                        <InlineShareBar ratio={(row.created / maxTeam) * 100} />
                      </td>
                      <td data-align="right">{formatInt(row.closed, uiLang)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </SectionCard>

        <SectionCard
          title={t("reports.ticket_by_assignee")}
          loading={loading}
          empty={!data?.by_assignee.length}
          minHeight={180}
          flush
        >
          <div className="rp-table-scroll">
            <table className="rp-table">
              <thead>
                <tr>
                  <th>{t("reports.col_assignee")}</th>
                  <th data-align="right">{t("reports.ticket_resolved")}</th>
                  <th data-align="right">{t("reports.col_open_now")}</th>
                  <th data-align="right">{t("reports.col_res_median")}</th>
                </tr>
              </thead>
              <tbody>
                {(data?.by_assignee ?? []).map((row) => (
                  <tr key={row.member_id} className="rp-row">
                    <td>
                      <Group gap="xs" wrap="nowrap">
                        <Avatar src={row.avatar_url || undefined} size="sm" radius="xl" name={row.name || row.member_id} color="initials" />
                        <Text size="sm" fw={500} truncate>
                          {row.name || row.member_id}
                        </Text>
                        {row.removed && (
                          <Badge size="xs" variant="light" color="gray">
                            {t("reports.member_removed")}
                          </Badge>
                        )}
                      </Group>
                    </td>
                    <td data-align="right">
                      <div>{formatInt(row.resolved, uiLang)}</div>
                      <InlineShareBar ratio={(row.resolved / maxAssignee) * 100} />
                    </td>
                    <td data-align="right">{formatInt(row.open_now, uiLang)}</td>
                    <td data-align="right">{formatDuration(row.resolution_median_seconds)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </SectionCard>

        <SectionCard
          title={t("reports.ticket_by_priority_visibility")}
          loading={loading}
          empty={!data?.by_priority.length && !data?.by_visibility.length}
          minHeight={180}
        >
          <Stack gap="md">
            <div>
              <Text size="xs" fw={600} c="dimmed" mb={6}>
                {t("reports.ticket_priority_label")}
              </Text>
              <Stack gap={6}>
                {(data?.by_priority ?? []).map((item) => {
                  const total = Math.max(1, (data?.by_priority ?? []).reduce((a, b) => a + b.created, 0))
                  return (
                    <div key={item.priority} className="rp-bar-row">
                      <Group justify="space-between" wrap="nowrap">
                        <Text size="sm">
                          {item.priority === "high" ? t("reports.priority_high") : t("reports.priority_normal")}
                        </Text>
                        <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                          {t("reports.created_and_open", {
                            created: formatInt(item.created, uiLang),
                            open: formatInt(item.open_now, uiLang),
                          })}
                        </Text>
                      </Group>
                      <Progress
                        value={(item.created / total) * 100}
                        size="xs"
                        color={item.priority === "high" ? "deep-orange" : "brand"}
                        radius="xl"
                      />
                    </div>
                  )
                })}
              </Stack>
            </div>
            <div>
              <Text size="xs" fw={600} c="dimmed" mb={6}>
                {t("reports.ticket_visibility_label")}
              </Text>
              <Stack gap={6}>
                {(data?.by_visibility ?? []).map((item) => {
                  const total = Math.max(1, (data?.by_visibility ?? []).reduce((a, b) => a + b.created, 0))
                  return (
                    <div key={item.visibility} className="rp-bar-row">
                      <Group justify="space-between" wrap="nowrap">
                        <Text size="sm">
                          {item.visibility === "all" ? t("reports.vis_all") : t("reports.vis_members")}
                        </Text>
                        <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                          {t("reports.created_and_open", {
                            created: formatInt(item.created, uiLang),
                            open: formatInt(item.open_now, uiLang),
                          })}
                        </Text>
                      </Group>
                      <Progress
                        value={(item.created / total) * 100}
                        size="xs"
                        color={item.visibility === "all" ? "bright-blue" : "gray"}
                        radius="xl"
                      />
                    </div>
                  )
                })}
              </Stack>
            </div>
          </Stack>
        </SectionCard>
      </SimpleGrid>
    </ReportShell>
  )
}
