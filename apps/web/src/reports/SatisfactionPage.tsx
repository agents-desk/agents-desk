// 满意度报表：CSAT、平均分、参评率、趋势图、1–5 星分布、团队/渠道拆解，以及支持分页与星级筛选的客户评价明细表。

import type { ReportRatingItem } from "@agents-desk/client/tenant"
import { AreaChart } from "@mantine/charts"
import { Badge, Group, Progress, SegmentedControl, SimpleGrid, Stack, Table, Text } from "@mantine/core"
import { IconMessageStar, IconMoodSmile, IconPercentage, IconStar, IconStarFilled } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate, useParams } from "@tanstack/react-router"
import {
  AnchorLink,
  type Column,
  DataTable,
  Page,
  type PeriodRoute,
  StatCard,
  TruncatedText,
  useFormatDateTime,
  useLanguage,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useCallback, useMemo } from "react"
import { kit } from "../api"
import { ReportFilterBar } from "./FilterBar"
import { csatColor, diffDelta, formatInt, formatPercent, formatScore, pctDelta, scoreColor } from "./format"
import { type SatisfactionSearchParams, useReportFilters } from "./search"
import { SectionCard } from "./SectionCard"

type RatingBucketFilter = "all" | "positive" | "neutral" | "negative"

/** 根据 min_score / max_score 映射到快捷星级档位。 */
function activeRatingFilter(min?: number, max?: number): RatingBucketFilter {
  if (min === 4 && (!max || max === 5)) return "positive"
  if (min === 3 && max === 3) return "neutral"
  if ((!min || min === 1) && max === 2) return "negative"
  return "all"
}

/** 满意度报表页面组件。 */
export function SatisfactionPage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const formatDateTime = useFormatDateTime()
  const navigate = useNavigate()
  const { slug } = useParams({ strict: false }) as { slug: string }

  const filters = useReportFilters(route)
  const search = route.useSearch() as SatisfactionSearchParams
  const page = search.page ?? 1
  const perPage = search.per_page ?? 20
  const minScore = search.min_score
  const maxScore = search.max_score

  const reportQuery = useQuery(kit.queries.report.satisfaction(filters.queryParams))
  const ratingsQuery = useQuery(
    kit.queries.report.satisfactionRatings({
      ...filters.queryParams,
      page,
      per_page: perPage,
      min_score: minScore,
      max_score: maxScore,
    }),
  )

  const data = reportQuery.data
  const loading = reportQuery.isPending
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

  const ratingFilterValue = activeRatingFilter(minScore, maxScore)
  const ratedTotal = Math.max(1, s?.rated_count ?? 0)

  const ratingColumns = useMemo<Column<ReportRatingItem>[]>(
    () => [
      {
        accessor: "score",
        title: t("reports.col_score"),
        width: 96,
        render: (row) => (
          <Badge size="sm" variant="light" color={scoreColor(row.score)} leftSection={<IconStarFilled size={11} />}>
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
        width: 140,
        render: (row) => (
          <Text size="sm" truncate>
            {row.contact_name || t("inbox.visitor")}
          </Text>
        ),
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
        width: 150,
        noWrap: true,
        render: (row) => (
          <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
            {formatDateTime(row.rated_at)}
          </Text>
        ),
      },
      {
        accessor: "conversation_id",
        title: "",
        width: 88,
        textAlign: "right",
        render: (row) => (
          <AnchorLink
            to="/t/$slug/inbox/$id"
            params={{ slug, id: row.conversation_id }}
            search={{ box: "all", status: "closed", sort: "waiting" }}
            size="xs"
          >
            {t("reports.open_conversation")}
          </AnchorLink>
        ),
      },
    ],
    [formatDateTime, slug, t],
  )

  return (
    <Page title={t("nav.reports_satisfaction")} description={t("reports.satisfaction_desc")} breadcrumbs={false}>
      <Stack gap="md">
        <ReportFilterBar
          period={filters.period}
          filters={filters}
          meta={data?.period}
          error={reportQuery.error ?? ratingsQuery.error}
        />

        <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="md">
          <StatCard
            label={t("reports.csat_label")}
            description={t("reports.csat_desc")}
            value={formatPercent(s?.csat)}
            delta={diffDelta(s?.csat, s?.prev_csat, (v) => t("reports.delta_pp", { v }))}
            secondary={
              s
                ? t("reports.csat_sub", {
                    pos: formatInt(s.positive_count, uiLang),
                    total: formatInt(s.rated_count, uiLang),
                  })
                : undefined
            }
            icon={<IconMoodSmile size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.avg_score")}
            value={formatScore(s?.avg_score)}
            delta={diffDelta(s?.avg_score, s?.prev_avg_score, (v) => v)}
            secondary={t("reports.score_out_of")}
            icon={<IconStar size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.rated_count")}
            value={formatInt(s?.rated_count, uiLang)}
            delta={pctDelta(s?.rated_count, s?.prev_rated_count)}
            icon={<IconMessageStar size={16} stroke={1.6} />}
            loading={loading}
          />
          <StatCard
            label={t("reports.response_rate")}
            value={formatPercent(s?.response_rate)}
            secondary={s ? t("reports.response_rate_sub", { closed: formatInt(s.closed_count, uiLang) }) : undefined}
            icon={<IconPercentage size={16} stroke={1.6} />}
            loading={loading}
          />
        </SimpleGrid>

        <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
          <SectionCard
            title={t("reports.csat_trend")}
            loading={loading}
            empty={!data?.series.length || !s?.rated_count}
            minHeight={240}
          >
            <AreaChart
              h={240}
              data={data?.series ?? []}
              dataKey="date"
              series={[{ name: "csat", label: t("reports.csat_label"), color: "green.6" }]}
              curveType="monotone"
              connectNulls
              withDots={false}
              gridAxis="xy"
              yAxisProps={{ domain: [0, 100] }}
              valueFormatter={(v) => `${v}%`}
            />
          </SectionCard>

          <SectionCard
            title={t("reports.score_distribution")}
            loading={loading}
            empty={!s?.rated_count}
            minHeight={240}
          >
            <Stack gap="sm" justify="center" mih={210}>
              {(data?.distribution ?? []).map((row) => {
                const share = s?.rated_count ? Math.round((row.count / ratedTotal) * 1000) / 10 : 0
                return (
                  <Group key={row.score} gap="sm" wrap="nowrap">
                    <Text size="sm" fw={500} w={52} style={{ flexShrink: 0 }}>
                      {t("reports.score_star", { n: row.score })}
                    </Text>
                    <Progress
                      value={share}
                      size="md"
                      radius="xl"
                      color={scoreColor(row.score)}
                      style={{ flex: 1 }}
                    />
                    <Text size="xs" c="dimmed" w={84} ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.count, uiLang)} ({share}%)
                    </Text>
                  </Group>
                )
              })}
            </Stack>
          </SectionCard>
        </SimpleGrid>

        <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
          <SectionCard title={t("reports.by_team")} loading={loading} empty={!data?.by_team.length} minHeight={160}>
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("reports.col_team")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_csat")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_avg_score")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_rated")}</Table.Th>
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
                    <Table.Td ta="right">
                      <Badge size="sm" variant="light" color={csatColor(row.csat)}>
                        {formatPercent(row.csat)}
                      </Badge>
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatScore(row.avg_score)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.rated_count, uiLang)}
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
            minHeight={160}
          >
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("reports.col_channel")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_csat")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_avg_score")}</Table.Th>
                  <Table.Th ta="right">{t("reports.col_rated")}</Table.Th>
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
                    <Table.Td ta="right">
                      <Badge size="sm" variant="light" color={csatColor(row.csat)}>
                        {formatPercent(row.csat)}
                      </Badge>
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatScore(row.avg_score)}
                    </Table.Td>
                    <Table.Td ta="right" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(row.rated_count, uiLang)}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </SectionCard>
        </SimpleGrid>

        <SectionCard
          title={t("reports.ratings_list")}
          action={
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
          }
        >
          <DataTable
            records={ratingsQuery.data?.items ?? []}
            columns={ratingColumns}
            rowKey="id"
            loading={ratingsQuery.isFetching}
            total={ratingsQuery.data?.total}
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
        </SectionCard>
      </Stack>
    </Page>
  )
}
