// 报表概览：顶部固定展示此刻全工作区实时健康状态，下方展示客服、工单、商务沟通与 AI 翻译的周期核心指标及融合趋势。

import { AreaChart } from "@mantine/charts"
import { Badge, Center, Group, SimpleGrid, Skeleton, Stack, Text, Tooltip } from "@mantine/core"
import { IconArrowUpRight } from "@tabler/icons-react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { Link, useParams } from "@tanstack/react-router"
import {
  LoadingStage,
  type PeriodRoute,
  type StatDelta,
  useCan,
  useFormatDuration,
  useLanguage,
  usePeriod,
  useSmoothTransition,
  useT,
} from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import { REPORT_AREA_PROPS } from "./Breakdown"
import { liveBacklogInboxSearch } from "./drilldown"
import { csatColor, formatInt, formatPercent, formatUSD, pctDelta } from "./format"
import { DeltaBadge } from "./MetricStrip"
import { GranularityControl } from "./PeriodButton"
import { ReportShell } from "./ReportShell"
import { SectionCard } from "./SectionCard"

/** 报表概览页面组件。 */
export function OverviewPage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const can = useCan()
  const uiLang = useLanguage()
  const formatDuration = useFormatDuration()
  const { slug } = useParams({ from: "/$slug" })
  const period = usePeriod(route, { defaultPreset: "7d" })
  const query = useQuery({
    ...kit.queries.report.overview(period.params),
    placeholderData: keepPreviousData,
  })

  const scopeKey = `${period.params.from}|${period.params.to}|${period.params.tz ?? ""}`
  const granKey = `${scopeKey}|${period.params.granularity ?? ""}`
  const pageState = useSmoothTransition(query.data, scopeKey, query.isFetching)
  const chartState = useSmoothTransition(query.data?.series, granKey, query.isFetching)
  const data = pageState.data
  const loading = query.isPending && !data
  const live = data?.live
  const svc = data?.service
  const internalSvc = data?.internal
  const tk = data?.tickets
  const biz = data?.business
  const ai = data?.ai
  const canConv = can("conversations:read")

  const liveItems: Array<{
    key: string
    label: string
    tip: string
    value: string
    warn?: boolean
    to?: "/$slug/inbox" | "/$slug/tickets" | "/$slug/chat" | "/$slug/reports/members"
    search?: Record<string, unknown>
  }> = [
    {
      key: "svc_waiting",
      label: t("reports.ov_live_service_waiting"),
      tip: t("reports.ov_live_service_waiting_tip"),
      value: formatInt(live?.service_waiting, uiLang),
      warn: Boolean(live && live.service_waiting > 0),
      to: canConv ? "/$slug/inbox" : undefined,
      search: liveBacklogInboxSearch("waiting", { audience: "customer" }) as unknown as Record<string, unknown>,
    },
    {
      key: "svc_unassigned",
      label: t("reports.ov_live_service_unassigned"),
      tip: t("reports.ov_live_service_unassigned_tip"),
      value: formatInt(live?.service_unassigned, uiLang),
      to: canConv ? "/$slug/inbox" : undefined,
      search: liveBacklogInboxSearch("unassigned", { audience: "customer" }) as unknown as Record<string, unknown>,
    },
    {
      key: "int_waiting",
      label: t("reports.ov_live_internal_waiting"),
      tip: t("reports.ov_live_internal_waiting_tip"),
      value: formatInt(live?.internal_waiting, uiLang),
      warn: Boolean(live && live.internal_waiting > 0),
      to: canConv ? "/$slug/inbox" : undefined,
      search: liveBacklogInboxSearch("waiting", { audience: "internal" }) as unknown as Record<string, unknown>,
    },
    {
      key: "tk_open",
      label: t("reports.ov_live_ticket_open"),
      tip: t("reports.ov_live_ticket_open_tip"),
      value: formatInt(live?.ticket_open, uiLang),
      warn: Boolean(live && live.ticket_unassigned > 0),
      to: canConv ? "/$slug/tickets" : undefined,
      search: { box: "all", status: "unresolved" },
    },
    {
      key: "biz_waiting",
      label: t("reports.ov_live_business_waiting"),
      tip: t("reports.ov_live_business_waiting_tip"),
      value: formatInt(live?.business_waiting, uiLang),
      warn: Boolean(live && live.business_waiting > 0),
      to: canConv ? "/$slug/chat" : undefined,
    },
    {
      key: "online",
      label: t("reports.ov_live_online"),
      tip: t("reports.ov_live_online_tip"),
      value: live ? `${formatInt(live.online_members, uiLang)} / ${formatInt(live.total_members, uiLang)}` : "—",
      to: "/$slug/reports/members",
    },
  ]

  const periodSearchParams = {
    preset: period.preset,
    from: period.from,
    to: period.to,
    granularity: period.granularity,
  }

  return (
    <ReportShell
      title={t("nav.reports_overview")}
      period={period}
      meta={data?.period}
      error={query.error}
      updating={pageState.busy}
    >
      {/* 此刻实时健康条：跨客服、工单、商务与成员在线状态，不受时间窗口影响 */}
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
                    {item.value}
                  </Text>
                )}
                {item.to && <IconArrowUpRight size={14} stroke={1.6} className="rp-live-arrow" />}
              </>
            )
            if (!item.to) {
              return (
                <span key={item.key} className="rp-live-item">
                  {inner}
                </span>
              )
            }
            return (
              <Tooltip key={item.key} label={item.tip} openDelay={250} withArrow>
                <Link
                  to={item.to}
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

      {/* 五大业务域周期汇总卡片：点击卡片直达对应专题报表 */}
      <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md">
        <OverviewDomainCard
          title={t("nav.reports_service")}
          to="/$slug/reports/service"
          slug={slug}
          search={periodSearchParams}
          loading={loading}
          primaryLabel={t("reports.conv_created")}
          primaryValue={formatInt(svc?.created, uiLang)}
          delta={pctDelta(svc?.created, svc?.prev_created)}
          rows={[
            { label: t("reports.closed_rounds"), value: formatInt(svc?.closed, uiLang) },
            { label: t("reports.frt_median"), value: formatDuration(svc?.first_reply_median_seconds) },
            {
              label: t("reports.col_csat"),
              value: svc?.rated_count ? (
                <Badge size="xs" variant="light" color={csatColor(svc.csat)}>
                  {formatPercent(svc.csat)}
                </Badge>
              ) : (
                "—"
              ),
            },
          ]}
        />

        <OverviewDomainCard
          title={t("nav.reports_internal")}
          to="/$slug/reports/internal"
          slug={slug}
          search={periodSearchParams}
          loading={loading}
          primaryLabel={t("reports.internal_created")}
          primaryValue={formatInt(internalSvc?.created, uiLang)}
          delta={pctDelta(internalSvc?.created, internalSvc?.prev_created)}
          rows={[
            { label: t("reports.closed_rounds"), value: formatInt(internalSvc?.closed, uiLang) },
            { label: t("reports.frt_median"), value: formatDuration(internalSvc?.first_reply_median_seconds) },
            { label: t("reports.internal_group_created"), value: formatInt(internalSvc?.group_created, uiLang) },
          ]}
        />

        <OverviewDomainCard
          title={t("nav.reports_tickets")}
          to="/$slug/reports/tickets"
          slug={slug}
          search={periodSearchParams}
          loading={loading}
          primaryLabel={t("reports.ticket_created")}
          primaryValue={formatInt(tk?.created, uiLang)}
          delta={pctDelta(tk?.created, tk?.prev_created)}
          rows={[
            { label: t("reports.ticket_resolved"), value: formatInt(tk?.resolved, uiLang) },
            { label: t("reports.ticket_res_median"), value: formatDuration(tk?.resolution_median_seconds) },
            { label: t("reports.ov_live_ticket_open"), value: formatInt(live?.ticket_open, uiLang) },
          ]}
        />

        <OverviewDomainCard
          title={t("nav.reports_business")}
          to="/$slug/reports/business"
          slug={slug}
          search={periodSearchParams}
          loading={loading}
          primaryLabel={t("reports.biz_messages")}
          primaryValue={formatInt(biz?.messages_total, uiLang)}
          delta={pctDelta(biz?.messages_total, biz?.prev_messages_total)}
          rows={[
            { label: t("reports.biz_active_chats"), value: formatInt(biz?.active_chats, uiLang) },
            { label: t("reports.biz_new_chats"), value: formatInt(biz?.new_chats, uiLang) },
            { label: t("reports.ov_live_business_24h"), value: formatInt(live?.business_active_24h, uiLang) },
          ]}
        />

        <OverviewDomainCard
          title={t("nav.reports_ai")}
          to="/$slug/reports/translation"
          slug={slug}
          search={periodSearchParams}
          loading={loading}
          primaryLabel={t("reports.translation_calls")}
          primaryValue={formatInt(ai?.calls, uiLang)}
          delta={pctDelta(ai?.calls, ai?.prev_calls)}
          rows={[
            { label: t("reports.success_rate"), value: formatPercent(ai?.success_rate) },
            { label: t("reports.col_failed"), value: formatInt(ai?.failed_calls, uiLang) },
            { label: t("reports.col_cost"), value: formatUSD(ai?.total_cost_usd) },
          ]}
        />
      </SimpleGrid>

      {/* 全局业务量趋势 */}
      <SectionCard
        title={t("reports.ov_series_title")}
        action={<GranularityControl period={period} />}
        loading={loading}
        empty={!chartState.data?.length}
        minHeight={260}
      >
        <LoadingStage loading={chartState.busy && !pageState.busy}>
          {!chartState.data?.length ? (
            <Center mih={240}>
              <Text size="sm" c="dimmed">
                {t("reports.empty")}
              </Text>
            </Center>
          ) : (
            <AreaChart
              h={260}
              data={chartState.data}
              dataKey="date"
              series={[
                { name: "service_created", label: t("reports.ov_series_service"), color: "brand.6" },
                { name: "internal_created", label: t("reports.ov_series_internal"), color: "violet.6" },
                { name: "ticket_created", label: t("reports.ov_series_tickets"), color: "deep-orange.6" },
                { name: "business_messages", label: t("reports.ov_series_business"), color: "bright-blue.6" },
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
      </SectionCard>
    </ReportShell>
  )
}

interface OverviewDomainCardProps {
  title: string
  to:
    | "/$slug/reports/service"
    | "/$slug/reports/internal"
    | "/$slug/reports/tickets"
    | "/$slug/reports/business"
    | "/$slug/reports/translation"
  slug: string
  search: Record<string, unknown>
  loading: boolean
  primaryLabel: string
  primaryValue: string
  delta?: StatDelta
  rows: Array<{ label: string; value: ReactNode }>
}

/** 概览页单个业务域汇总卡片，点击可携带当前时间窗口直达专题报表。 */
function OverviewDomainCard({
  title,
  to,
  slug,
  search,
  loading,
  primaryLabel,
  primaryValue,
  delta,
  rows,
}: OverviewDomainCardProps): ReactNode {
  return (
    <Link to={to} params={{ slug }} search={search as never} className="rp-ov-card">
      <Stack gap="sm">
        <Group justify="space-between" align="center" wrap="nowrap">
          <Text size="sm" fw={600}>
            {title}
          </Text>
          <IconArrowUpRight size={15} stroke={1.6} className="rp-ov-card-arrow" />
        </Group>

        <div>
          <Text size="xs" c="dimmed">
            {primaryLabel}
          </Text>
          {loading ? (
            <Skeleton height={28} width={88} mt={4} radius="xs" />
          ) : (
            <Group gap={8} align="baseline" mt={2} wrap="nowrap">
              <Text size="xl" fw={700} style={{ fontVariantNumeric: "tabular-nums", lineHeight: 1.2 }}>
                {primaryValue}
              </Text>
              <DeltaBadge delta={delta} />
            </Group>
          )}
        </div>

        <Stack gap={6} pt="xs" style={{ borderTop: "1px solid var(--tn-border)" }}>
          {rows.map((r) => (
            <Group key={r.label} justify="space-between" align="center" wrap="nowrap">
              <Text size="xs" c="dimmed">
                {r.label}
              </Text>
              {loading ? (
                <Skeleton height={14} width={44} radius="xs" />
              ) : (
                <Text component="div" size="xs" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
                  {r.value}
                </Text>
              )}
            </Group>
          ))}
        </Stack>
      </Stack>
    </Link>
  )
}
