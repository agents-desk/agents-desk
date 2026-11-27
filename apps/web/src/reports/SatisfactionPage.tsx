// 满意度报表：CSAT 指标带与趋势/可点击星级分布融合主面板、团队/渠道对比，以及客户评价明细表。

import type { ReportRatingItem } from "@agents-desk/client/tenant"
import { AreaChart } from "@mantine/charts"
import {
  Badge,
  Center,
  CloseButton,
  Group,
  Progress,
  SegmentedControl,
  SimpleGrid,
  Skeleton,
  Stack,
  Text,
  Tooltip,
} from "@mantine/core"
import { IconStarFilled } from "@tabler/icons-react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { Link, useNavigate, useParams } from "@tanstack/react-router"
import {
  AnchorLink,
  type Column,
  DataTable,
  LoadingStage,
  type PeriodRoute,
  TruncatedText,
  useCan,
  useFormatDateTime,
  useLanguage,
  useSmoothTransition,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useCallback, useMemo, useRef } from "react"
import { kit } from "../api"
import { InboxDrillButton, REPORT_AREA_PROPS } from "./Breakdown"
import { channelInboxSearch, teamInboxSearch } from "./drilldown"
import { csatColor, diffDelta, formatInt, formatPercent, formatScore, pctDelta, scoreColor } from "./format"
import { type MetricItem, MetricStrip } from "./MetricStrip"
import { GranularityControl } from "./PeriodButton"
import { ReportShell } from "./ReportShell"
import { type SatisfactionSearchParams, useReportFilters } from "./search"
import { DetailSection, SectionCard } from "./SectionCard"

type RatingBucketFilter = "all" | "positive" | "neutral" | "negative"

/** 根据 min_score / max_score 映射到快捷星级档位；单星筛选（1/2/4/5）时返回空串，档位都不高亮。 */
function activeRatingFilter(min?: number, max?: number): RatingBucketFilter | "" {
  if (!min && !max) return "all"
  if (min === 4 && (!max || max === 5)) return "positive"
  if (min === 3 && max === 3) return "neutral"
  if ((!min || min === 1) && max === 2) return "negative"
  return ""
}

/** 满意度报表页面组件（仅统计对外客户服务）。 */
export function SatisfactionPage({
  route,
  audience = "customer",
  title,
  toolbarRight,
}: {
  route: PeriodRoute
  audience?: "customer" | "internal"
  title?: ReactNode
  toolbarRight?: ReactNode
}): ReactNode {
  const t = useT()
  const can = useCan()
  const uiLang = useLanguage()
  const formatDateTime = useFormatDateTime()
  const navigate = useNavigate()
  const { slug } = useParams({ from: "/$slug" })
  const ratingsSectionRef = useRef<HTMLDivElement>(null)

  const filters = useReportFilters(route, { defaultPreset: "7d", audience })
  const search = route.useSearch() as SatisfactionSearchParams
  const page = search.page ?? 1
  const perPage = search.per_page ?? 20
  const minScore = search.min_score
  const maxScore = search.max_score
  const singleStar = minScore !== undefined && minScore === maxScore ? minScore : undefined
  const canInbox = can("conversations:read")
  const canContacts = can("contacts:read")
  const scope = { audience, teamId: filters.teamId, channelId: filters.channelId }

  const reportQuery = useQuery({
    ...kit.queries.report.satisfaction(filters.queryParams),
    placeholderData: keepPreviousData,
  })
  const ratingsQuery = useQuery({
    ...kit.queries.report.satisfactionRatings({
      ...filters.queryParams,
      page,
      per_page: perPage,
      min_score: minScore,
      max_score: maxScore,
    }),
    placeholderData: keepPreviousData,
  })

  const scopeKey = `${filters.queryParams.from}|${filters.queryParams.to}|${filters.queryParams.tz ?? ""}|${filters.teamId ?? ""}|${filters.channelId ?? ""}`
  const granKey = `${scopeKey}|${filters.queryParams.granularity ?? ""}`
  const ratingsKey = `${scopeKey}|${page}|${perPage}|${minScore ?? ""}|${maxScore ?? ""}`

  const pageState = useSmoothTransition(reportQuery.data, scopeKey, reportQuery.isFetching)
  const chartState = useSmoothTransition(reportQuery.data?.series, granKey, reportQuery.isFetching)
  const ratingsState = useSmoothTransition(ratingsQuery.data, ratingsKey, ratingsQuery.isFetching)

  const data = pageState.data
  const loading = reportQuery.isPending && !data
  const s = data?.summary

  const updateRatingsSearch = useCallback(
    (patch: Record<string, unknown>) => {
      void navigate({
        to: ".",
        search: ((prev: Record<string, unknown>) => {
          const next: Record<string, unknown> = { ...prev }
          for (const [k, v] of Object.entries(patch)) {
            if (v === undefined) delete next[k]
            else next[k] = v
          }
          return next
        }) as never,
        replace: true,
      })
    },
    [navigate],
  )

  const setRatingFilter = useCallback(
    (v: RatingBucketFilter) => {
      switch (v) {
        case "positive":
          updateRatingsSearch({ min_score: 4, max_score: undefined, page: undefined })
          break
        case "neutral":
          updateRatingsSearch({ min_score: 3, max_score: 3, page: undefined })
          break
        case "negative":
          updateRatingsSearch({ min_score: undefined, max_score: 2, page: undefined })
          break
        default:
          updateRatingsSearch({ min_score: undefined, max_score: undefined, page: undefined })
      }
    },
    [updateRatingsSearch],
  )

  const toggleSingleStar = useCallback(
    (star: number) => {
      if (singleStar === star) {
        updateRatingsSearch({ min_score: undefined, max_score: undefined, page: undefined })
        return
      }
      updateRatingsSearch({ min_score: star, max_score: star, page: undefined })
      ratingsSectionRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" })
    },
    [singleStar, updateRatingsSearch],
  )

  const ratingFilterValue = activeRatingFilter(minScore, maxScore)
  const ratedTotal = Math.max(1, s?.rated_count ?? 0)

  const metrics: MetricItem[] = [
    {
      key: "csat",
      label: t("reports.csat_label"),
      description: t("reports.csat_desc"),
      value: formatPercent(s?.csat),
      delta: diffDelta(s?.csat, s?.prev_csat, (v) => t("reports.delta_pp", { v })),
      secondary: s
        ? t("reports.csat_sub", {
            pos: formatInt(s.positive_count, uiLang),
            total: formatInt(s.rated_count, uiLang),
          })
        : undefined,
    },
    {
      key: "avg_score",
      label: t("reports.avg_score"),
      value: formatScore(s?.avg_score),
      delta: diffDelta(s?.avg_score, s?.prev_avg_score, (v) => v),
      secondary: t("reports.score_out_of"),
    },
    {
      key: "rated_count",
      label: t("reports.rated_count"),
      value: formatInt(s?.rated_count, uiLang),
      delta: pctDelta(s?.rated_count, s?.prev_rated_count),
    },
    {
      key: "response_rate",
      label: t("reports.response_rate"),
      description: t("reports.response_rate_desc"),
      value: formatPercent(s?.response_rate),
      secondary: s ? t("reports.response_rate_sub", { closed: formatInt(s.closed_count, uiLang) }) : undefined,
    },
  ]

  const ratingColumns = useMemo<Column<ReportRatingItem>[]>(
    () => [
      {
        accessor: "score",
        title: t("reports.col_score"),
        width: 92,
        render: (row) => (
          <Badge size="sm" variant="light" color={scoreColor(row.score)} leftSection={<IconStarFilled size={12} />}>
            {row.score}
          </Badge>
        ),
      },
      {
        accessor: "comment",
        title: t("reports.col_comment"),
        render: (row) =>
          row.comment ? (
            <TruncatedText value={row.comment} />
          ) : (
            <Text size="xs" c="dimmed">
              {t("reports.no_comment")}
            </Text>
          ),
      },
      {
        accessor: "contact_name",
        title: t("reports.col_contact"),
        width: 148,
        render: (row) => {
          const name = row.contact_name || t("inbox.visitor")
          if (canContacts && row.contact_id) {
            return (
              <Tooltip label={t("reports.drill_contact")} openDelay={300} withArrow>
                <Link
                  to="/$slug/contacts/$id"
                  params={{ slug, id: row.contact_id }}
                  search={{ sort: "recent" }}
                  className="rp-drill-link"
                >
                  <Text span size="sm" truncate>
                    {name}
                  </Text>
                </Link>
              </Tooltip>
            )
          }
          return (
            <Text size="sm" truncate>
              {name}
            </Text>
          )
        },
      },
      {
        accessor: "team_name",
        title: t("reports.col_team"),
        width: 130,
        render: (row) => (
          <Text size="sm" c={row.team_name ? undefined : "dimmed"} truncate>
            {row.team_name || t("reports.no_team")}
          </Text>
        ),
      },
      {
        accessor: "assignee_name",
        title: t("reports.col_assignee"),
        width: 120,
        render: (row) => (
          <Text size="sm" c={row.assignee_name ? undefined : "dimmed"} truncate>
            {row.assignee_name || "—"}
          </Text>
        ),
      },
      {
        accessor: "round",
        title: t("reports.col_round"),
        width: 84,
        render: (row) => (
          <Badge size="xs" variant="light" color="gray">
            {t("reports.round_badge", { n: row.round })}
          </Badge>
        ),
      },
      {
        accessor: "rated_at",
        title: t("reports.col_rated_at"),
        width: 146,
        noWrap: true,
        render: (row) => (
          <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
            {formatDateTime(row.rated_at)}
          </Text>
        ),
      },
      ...(canInbox
        ? [
            {
              accessor: "conversation_id" as const,
              title: "",
              width: 84,
              textAlign: "right" as const,
              render: (row: ReportRatingItem) => (
                <AnchorLink
                  to="/$slug/inbox/$id"
                  params={{ slug, id: row.conversation_id }}
                  search={{ box: "all", status: "closed", sort: "waiting" }}
                  size="xs"
                >
                  {t("reports.open_conversation")}
                </AnchorLink>
              ),
            },
          ]
        : []),
    ],
    [canContacts, canInbox, formatDateTime, slug, t],
  )

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
      error={reportQuery.error ?? ratingsQuery.error}
      updating={pageState.busy}
      toolbarRight={toolbarRight}
    >
      {/* 主分析面板：指标带 + 左右双栏（CSAT 趋势 + 可点击星级分布） */}
      <section className="rp-panel">
        <MetricStrip items={metrics} loading={loading} />
        <div className="rp-panel-split">
          <div className="rp-panel-split-pane">
            <Group justify="space-between" align="center" gap="xs" mb="sm" wrap="wrap">
              <Text size="sm" fw={600}>
                {t("reports.csat_trend")}
              </Text>
              <GranularityControl period={filters.period} />
            </Group>
            <LoadingStage loading={chartState.busy && !pageState.busy}>
              {loading ? (
                <Skeleton height={220} radius="sm" />
              ) : !chartState.data?.length || !s?.rated_count ? (
                <Center mih={220}>
                  <Text size="sm" c="dimmed">
                    {t("reports.empty")}
                  </Text>
                </Center>
              ) : (
                <AreaChart
                  h={220}
                  data={chartState.data}
                  dataKey="date"
                  series={[{ name: "csat", label: t("reports.csat_label"), color: "green.6" }]}
                  curveType="monotone"
                  strokeWidth={2}
                  fillOpacity={0.14}
                  areaProps={REPORT_AREA_PROPS}
                  connectNulls
                  withDots={false}
                  gridAxis="y"
                  yAxisProps={{ domain: [0, 100] }}
                  valueFormatter={(v) => `${v}%`}
                />
              )}
            </LoadingStage>
          </div>

          <div className="rp-panel-split-pane">
            <Group justify="space-between" align="center" gap="xs" mb="sm" wrap="wrap">
              <Text size="sm" fw={600}>
                {t("reports.score_distribution")}
              </Text>
              <Text size="xs" c="dimmed">
                {t("reports.score_filter_hint")}
              </Text>
            </Group>
            {loading ? (
              <Skeleton height={220} radius="sm" />
            ) : !s?.rated_count ? (
              <Center mih={220}>
                <Text size="sm" c="dimmed">
                  {t("reports.empty")}
                </Text>
              </Center>
            ) : (
              <Stack gap={4} justify="center" mih={210}>
                {(data?.distribution ?? []).map((row) => {
                  const share = s.rated_count ? Math.round((row.count / ratedTotal) * 1000) / 10 : 0
                  const selected = singleStar === row.score
                  return (
                    <button
                      key={row.score}
                      type="button"
                      className="rp-bar-row"
                      data-clickable=""
                      data-selected={selected || undefined}
                      aria-pressed={selected}
                      onClick={() => toggleSingleStar(row.score)}
                    >
                      <Group gap="sm" wrap="nowrap" w="100%">
                        <Text size="xs" fw={500} w={42} style={{ flexShrink: 0 }}>
                          {t("reports.score_star", { n: row.score })}
                        </Text>
                        <Progress
                          value={share}
                          size="sm"
                          radius="xl"
                          color={scoreColor(row.score)}
                          style={{ flex: 1 }}
                        />
                        <Text size="xs" c="dimmed" w={82} ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                          {formatInt(row.count, uiLang)} ({share}%)
                        </Text>
                      </Group>
                    </button>
                  )
                })}
              </Stack>
            )}
          </div>
        </div>
      </section>

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
                <th data-align="right">{t("reports.col_csat")}</th>
                <th data-align="right">{t("reports.col_avg_score")}</th>
                <th data-align="right">{t("reports.col_rated")}</th>
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
                      <Badge size="sm" variant="light" color={csatColor(row.csat)}>
                        {formatPercent(row.csat)}
                      </Badge>
                    </td>
                    <td data-align="right">{formatScore(row.avg_score)}</td>
                    <td data-align="right">{formatInt(row.rated_count, uiLang)}</td>
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
                <th data-align="right">{t("reports.col_csat")}</th>
                <th data-align="right">{t("reports.col_avg_score")}</th>
                <th data-align="right">{t("reports.col_rated")}</th>
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
                    <td data-align="right">
                      <Badge size="sm" variant="light" color={csatColor(row.csat)}>
                        {formatPercent(row.csat)}
                      </Badge>
                    </td>
                    <td data-align="right">{formatScore(row.avg_score)}</td>
                    <td data-align="right">{formatInt(row.rated_count, uiLang)}</td>
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

      <div ref={ratingsSectionRef}>
        <DetailSection
          title={t("reports.ratings_list")}
          action={
            <Group gap="xs" wrap="wrap">
              {singleStar !== undefined && singleStar !== 3 && (
                <Badge
                  size="sm"
                  variant="light"
                  color={scoreColor(singleStar)}
                  rightSection={
                    <CloseButton
                      size={14}
                      onClick={() => updateRatingsSearch({ min_score: undefined, max_score: undefined, page: undefined })}
                    />
                  }
                >
                  {t("reports.score_only_badge", { n: singleStar })}
                </Badge>
              )}
              <SegmentedControl
                size="xs"
                value={ratingFilterValue}
                onChange={(v) => setRatingFilter(v as RatingBucketFilter)}
                data={[
                  { value: "all", label: t("reports.filter_all_ratings") },
                  { value: "positive", label: t("reports.filter_positive") },
                  { value: "neutral", label: t("reports.filter_neutral") },
                  { value: "negative", label: t("reports.filter_negative") },
                ]}
              />
            </Group>
          }
        >
          <DataTable
            records={ratingsState.data?.items ?? []}
            columns={ratingColumns}
            rowKey="id"
            loading={(ratingsQuery.isPending && !ratingsState.data) || (ratingsState.busy && !pageState.busy)}
            total={ratingsState.data?.total}
            list={{
              page,
              perPage,
              sort: undefined,
              setPage: (p) => updateRatingsSearch({ page: p > 1 ? p : undefined }),
              setPerPage: (pp) => updateRatingsSearch({ per_page: pp === 20 ? undefined : pp, page: undefined }),
              setSort: () => {},
            }}
            empty={t("reports.empty")}
          />
        </DetailSection>
      </div>
    </ReportShell>
  )
}
