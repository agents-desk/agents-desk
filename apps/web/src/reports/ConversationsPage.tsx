// 会话报表：顶部实时积压状态条（直达收件箱）、周期核心指标带与趋势融合面板，以及支持聚焦筛选与下钻的多维拆解。

import { AreaChart } from "@mantine/charts"
import { Badge, Center, Group, Progress, SimpleGrid, Skeleton, Stack, Text, Tooltip } from "@mantine/core"
import { IconArrowUpRight } from "@tabler/icons-react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { Link, useParams } from "@tanstack/react-router"
import { LoadingStage, type PeriodRoute, useCan, useLanguage, useSmoothTransition, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import { languageName } from "../languages"
import { InboxDrillButton, InlineShareBar, REPORT_AREA_PROPS } from "./Breakdown"
import {
  channelInboxSearch,
  languageInboxSearch,
  liveBacklogInboxSearch,
  tagInboxSearch,
  teamInboxSearch,
} from "./drilldown"
import { formatInt, pctDelta } from "./format"
import { type MetricItem, MetricStrip } from "./MetricStrip"
import { GranularityControl } from "./PeriodButton"
import { ReportShell } from "./ReportShell"
import { useReportFilters } from "./search"
import { SectionCard } from "./SectionCard"

/** 会话报表页面组件（同时服务对外客服、合作方服务与内部服务场景）。 */
export function ConversationsPage({
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
  const { slug } = useParams({ from: "/$slug" })
  const filters = useReportFilters(route, { defaultPreset: "7d", audience })
  const query = useQuery({
    ...kit.queries.report.conversations(filters.queryParams),
    placeholderData: keepPreviousData,
  })
  const scopeKey = `${filters.queryParams.from}|${filters.queryParams.to}|${filters.queryParams.tz ?? ""}|${audience ?? ""}|${filters.teamId ?? ""}|${filters.channelId ?? ""}`
  const granKey = `${scopeKey}|${filters.queryParams.granularity ?? ""}`
  const pageState = useSmoothTransition(query.data, scopeKey, query.isFetching)
  const chartState = useSmoothTransition(query.data?.series, granKey, query.isFetching)
  const data = pageState.data
  const loading = query.isPending && !data
  const s = data?.summary
  const canInbox = can("conversations:read")
  const scope = { audience, teamId: filters.teamId, channelId: filters.channelId }
  const isInternal = audience === "internal"
  const isPartner = audience === "partner"

  const maxChannel = Math.max(1, ...(data?.by_channel ?? []).map((x) => x.created))
  const maxTeam = Math.max(1, ...(data?.by_team ?? []).map((x) => x.created))
  const maxTag = Math.max(1, ...(data?.by_tag ?? []).map((x) => x.count))
  const langTotal = (data?.by_language ?? []).reduce((acc, x) => acc + x.count, 0)
  const maxLang = Math.max(1, ...(data?.by_language ?? []).map((x) => x.count))

  const liveItems: Array<{
    key: "open" | "waiting" | "unassigned" | "snoozed"
    label: string
    tip: string
    value: number | undefined
    warn?: boolean
  }> = [
    { key: "open", label: t("reports.live_open"), tip: t("reports.live_open_tip"), value: s?.open_now },
    {
      key: "waiting",
      label: t("reports.live_waiting"),
      tip: t("reports.live_waiting_tip"),
      value: s?.waiting_now,
      warn: Boolean(s && s.waiting_now > 0),
    },
    {
      key: "unassigned",
      label: t("reports.live_unassigned"),
      tip: t("reports.live_unassigned_tip"),
      value: s?.unassigned_now,
    },
    { key: "snoozed", label: t("reports.live_snoozed"), tip: t("reports.live_snoozed_tip"), value: s?.snoozed_now },
  ]

  const metrics: MetricItem[] = [
    {
      key: "created",
      label: isInternal ? t("reports.internal_created") : isPartner ? t("reports.partner_created") : t("reports.conv_created"),
      description: isInternal
        ? t("reports.internal_created_desc")
        : isPartner
          ? t("reports.partner_created_desc")
          : t("reports.conv_created_desc"),
      value: formatInt(s?.created, uiLang),
      delta: pctDelta(s?.created, s?.prev_created),
      secondary: s
        ? t(
            isInternal
              ? "reports.conv_target_sub_internal"
              : isPartner
                ? "reports.conv_target_sub_partner"
                : "reports.conv_target_sub",
            {
              direct: formatInt(s.direct_created, uiLang),
              group: formatInt(s.group_created, uiLang),
            },
          )
        : undefined,
    },
    {
      key: "rounds_opened",
      label: t("reports.conv_rounds_opened"),
      description: t("reports.conv_rounds_opened_desc"),
      value: formatInt(s?.rounds_opened, uiLang),
      delta: pctDelta(s?.rounds_opened, s?.prev_rounds_opened),
    },
    {
      key: "reopened",
      label: t("reports.conv_reopened"),
      description: t("reports.conv_reopened_desc"),
      value: formatInt(s?.reopened, uiLang),
      delta: pctDelta(s?.reopened, s?.prev_reopened, true),
    },
    {
      key: "closed",
      label: t("reports.closed_rounds"),
      value: formatInt(s?.closed, uiLang),
      delta: pctDelta(s?.closed, s?.prev_closed),
    },
  ]

  const toggleTeam = (id: string | undefined) => {
    if (!id) return
    filters.setTeamId(filters.teamId === id ? undefined : id)
  }

  const toggleChannel = (id: string) => {
    filters.setChannelId(filters.channelId === id ? undefined : id)
  }

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
      {/* 实时积压状态条：与周期趋势独立，每一项直接进入收件箱对应实时视图 */}
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
                {canInbox && <IconArrowUpRight size={14} stroke={1.6} className="rp-live-arrow" />}
              </>
            )
            if (!canInbox) {
              return (
                <span key={item.key} className="rp-live-item">
                  {inner}
                </span>
              )
            }
            return (
              <Tooltip key={item.key} label={item.tip} openDelay={250} withArrow>
                <Link
                  to="/$slug/inbox"
                  params={{ slug }}
                  search={liveBacklogInboxSearch(item.key, scope)}
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

      {/* 主分析面板：周期等分指标带 + 会话趋势面积图 */}
      <section className="rp-panel">
        <MetricStrip items={metrics} loading={loading} />
        <div className="rp-panel-body">
          <Group justify="space-between" align="center" gap="xs" mb="sm" wrap="wrap">
            <Text size="sm" fw={600}>
              {t("reports.conv_series_title")}
            </Text>
            <GranularityControl period={filters.period} />
          </Group>
          <LoadingStage loading={chartState.busy && !pageState.busy}>
            {loading ? (
              <Skeleton height={240} radius="sm" />
            ) : !chartState.data?.length ? (
              <Center mih={240}>
                <Text size="sm" c="dimmed">
                  {t("reports.empty")}
                </Text>
              </Center>
            ) : (
              <AreaChart
                h={240}
                data={chartState.data}
                dataKey="date"
                series={[
                  { name: "created", label: t("reports.series_created"), color: "brand.6" },
                  { name: "closed", label: t("reports.series_closed"), color: "green.6" },
                  { name: "reopened", label: t("reports.series_reopened"), color: "deep-orange.6" },
                ]}
                curveType="monotone"
                strokeWidth={2}
                fillOpacity={0.14}
                areaProps={REPORT_AREA_PROPS}
                withLegend
                withDots={false}
                gridAxis="y"
              />
            )}
          </LoadingStage>
        </div>
      </section>

      {/* 多维拆解 2×2 */}
      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
        <SectionCard
          title={t("reports.by_channel")}
          action={
            <Text size="xs" c="dimmed">
              {filters.channelId ? t("reports.focus_clear_hint") : t("reports.focus_hint")}
            </Text>
          }
          loading={loading}
          empty={!data?.by_channel.length}
          minHeight={180}
          flush
        >
          <table className="rp-table">
            <thead>
              <tr>
                <th>{t("reports.col_channel")}</th>
                <th>{t("reports.col_kind")}</th>
                <th data-align="right">{t("reports.col_created")}</th>
                <th data-align="right">{t("reports.col_closed")}</th>
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
                    <td>
                      <Badge size="xs" variant="light" color="gray">
                        {kit.label("ChannelKind", row.kind)}
                      </Badge>
                    </td>
                    <td data-align="right">
                      <div>{formatInt(row.created, uiLang)}</div>
                      <InlineShareBar ratio={(row.created / maxChannel) * 100} />
                    </td>
                    <td data-align="right">{formatInt(row.closed, uiLang)}</td>
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

        <SectionCard
          title={t("reports.by_team")}
          action={
            <Text size="xs" c="dimmed">
              {filters.teamId ? t("reports.focus_clear_hint") : t("reports.focus_hint")}
            </Text>
          }
          loading={loading}
          empty={!data?.by_team.length}
          minHeight={180}
          flush
        >
          <table className="rp-table">
            <thead>
              <tr>
                <th>{t("reports.col_team")}</th>
                <th data-align="right">{t("reports.col_created")}</th>
                <th data-align="right">{t("reports.col_closed")}</th>
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
                    <td data-align="right">
                      <div>{formatInt(row.created, uiLang)}</div>
                      <InlineShareBar ratio={(row.created / maxTeam) * 100} />
                    </td>
                    <td data-align="right">{formatInt(row.closed, uiLang)}</td>
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

        <SectionCard title={t("reports.by_tag")} loading={loading} empty={!data?.by_tag.length} minHeight={180}>
          <Stack gap={4}>
            {(data?.by_tag ?? []).map((tag) => {
              const body = (
                <>
                  <Group justify="space-between" wrap="nowrap">
                    <Badge size="sm" variant="light" color={tag.color || "gray"}>
                      {tag.name}
                    </Badge>
                    <Group gap={4} wrap="nowrap">
                      <Text size="xs" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
                        {formatInt(tag.count, uiLang)}
                      </Text>
                      {canInbox && <IconArrowUpRight size={14} stroke={1.6} className="rp-bar-row-arrow" />}
                    </Group>
                  </Group>
                  <Progress value={(tag.count / maxTag) * 100} size="xs" color={tag.color || "brand"} radius="xl" />
                </>
              )
              if (!canInbox) {
                return (
                  <div key={tag.id} className="rp-bar-row">
                    {body}
                  </div>
                )
              }
              return (
                <Tooltip key={tag.id} label={t("reports.drill_tag_inbox")} openDelay={300} withArrow>
                  <Link
                    to="/$slug/inbox"
                    params={{ slug }}
                    search={tagInboxSearch(tag.id, scope)}
                    className="rp-bar-row"
                    data-clickable=""
                  >
                    {body}
                  </Link>
                </Tooltip>
              )
            })}
          </Stack>
        </SectionCard>

        <SectionCard
          title={t("reports.by_language")}
          loading={loading}
          empty={!data?.by_language.length}
          minHeight={180}
        >
          <Stack gap={4}>
            {(data?.by_language ?? []).map((item) => {
              const unknown = item.language === "und" || !item.language
              const label = unknown ? t("reports.lang_unknown") : languageName(item.language, uiLang)
              const share = langTotal > 0 ? Math.round((item.count / langTotal) * 1000) / 10 : 0
              const clickable = canInbox && !unknown
              const body = (
                <>
                  <Group justify="space-between" wrap="nowrap">
                    <Text size="sm" c={unknown ? "dimmed" : undefined} truncate>
                      {label}
                    </Text>
                    <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
                      <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                        {formatInt(item.count, uiLang)} ({share}%)
                      </Text>
                      {clickable && <IconArrowUpRight size={14} stroke={1.6} className="rp-bar-row-arrow" />}
                    </Group>
                  </Group>
                  <Progress
                    value={(item.count / maxLang) * 100}
                    size="xs"
                    color={unknown ? "gray" : "bright-blue"}
                    radius="xl"
                  />
                </>
              )
              if (!clickable) {
                return (
                  <div key={item.language || "__unknown__"} className="rp-bar-row">
                    {body}
                  </div>
                )
              }
              return (
                <Tooltip key={item.language} label={t("reports.drill_language_inbox")} openDelay={300} withArrow>
                  <Link
                    to="/$slug/inbox"
                    params={{ slug }}
                    search={languageInboxSearch(item.language, scope)}
                    className="rp-bar-row"
                    data-clickable=""
                  >
                    {body}
                  </Link>
                </Tooltip>
              )
            })}
          </Stack>
        </SectionCard>
      </SimpleGrid>
    </ReportShell>
  )
}
