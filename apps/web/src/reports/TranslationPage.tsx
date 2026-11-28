// 翻译报表：指标带直接驱动趋势曲线切换、按场景/模型拆解，以及支持下钻收件箱的识别语言分布。

import { AreaChart } from "@mantine/charts"
import { Badge, Button, Group, Progress, SegmentedControl, SimpleGrid, Skeleton, Stack, Text, ThemeIcon, Tooltip } from "@mantine/core"
import { IconArrowUpRight, IconRobot, IconSparkles } from "@tabler/icons-react"
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
import { type ReactNode, useMemo, useState } from "react"
import { kit } from "../api"
import { useAiWorkspaceEnabled } from "../components/ai-placeholders"
import { languageName } from "../languages"
import { InlineShareBar, REPORT_AREA_PROPS } from "./Breakdown"
import { languageInboxSearch } from "./drilldown"
import { formatInt, formatPercent, formatUSD, pctDelta } from "./format"
import { GranularityControl } from "./PeriodButton"
import { ReportShell } from "./ReportShell"
import type { TranslationReportSearchParams } from "./search"
import { ReportEmpty, SectionCard } from "./SectionCard"

type TrendMetric = "calls" | "tokens" | "cost"
type AiReportTab = "translation" | "agent"

/** 翻译与 AI 效能报表页面组件。 */
export function TranslationPage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const can = useCan()
  const uiLang = useLanguage()
  const formatDuration = useFormatDuration()
  const navigate = useNavigate()
  const { slug } = useParams({ from: "/$slug" })
  const period = usePeriod(route, { defaultPreset: "7d" })
  const search = route.useSearch() as TranslationReportSearchParams
  const aiEnabled = useAiWorkspaceEnabled()
  const tab: AiReportTab = aiEnabled && search.tab === "agent" ? "agent" : "translation"
  const query = useQuery({
    ...kit.queries.report.translation(period.params),
    placeholderData: keepPreviousData,
    enabled: tab === "translation",
  })

  const setTab = (next: string) => {
    void navigate({
      to: ".",
      search: ((prev: Record<string, unknown>) => {
        const out: Record<string, unknown> = { ...prev }
        if (next === "translation") delete out.tab
        else out.tab = next
        return out
      }) as never,
      replace: true,
    })
  }

  const tabSwitcher = (
    <SegmentedControl
      value={tab}
      onChange={setTab}
      data={[
        { value: "translation", label: t("reports.ai_tab_translation") },
        { value: "agent", label: t("reports.ai_tab_agent") },
      ]}
    />
  )
  const [metric, setMetric] = useState<TrendMetric>("calls")
  const scopeKey = `${period.params.from}|${period.params.to}|${period.params.tz ?? ""}`
  const chartKey = `${scopeKey}|${period.params.granularity ?? ""}|${metric}`
  const pageState = useSmoothTransition(query.data, scopeKey, query.isFetching)
  const chartValue = useMemo(() => ({ series: query.data?.series, metric }), [query.data?.series, metric])
  const chartState = useSmoothTransition(chartValue, chartKey, query.isFetching)
  const data = pageState.data
  const loading = query.isPending && !data
  const s = data?.summary
  const canInbox = can("conversations:read")

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

  const sceneLabel = (scene: string): string => {
    switch (scene) {
      case "service":
        return t("reports.scene_service")
      case "partner_service":
        return t("reports.scene_partner_service")
      case "internal_service":
        return t("reports.scene_internal_service")
      case "ticket":
        return t("reports.scene_ticket")
      case "business":
        return t("reports.scene_business")
      case "internal_chat":
        return t("reports.scene_internal_chat")
      default:
        return t("reports.op_other")
    }
  }

  const maxSceneCalls = Math.max(1, ...(data?.by_scene ?? []).map((x) => x.calls))
  const maxOpCalls = Math.max(1, ...(data?.by_operation ?? []).map((x) => x.calls))
  const maxModelCalls = Math.max(1, ...(data?.by_model ?? []).map((x) => x.calls))
  const maxDetected = Math.max(1, ...(data?.detected_languages ?? []).map((x) => x.count))
  const maxReply = Math.max(1, ...(data?.reply_languages ?? []).map((x) => x.count))

  const activeChartMetric = chartState.data.metric
  const seriesConfig =
    activeChartMetric === "calls"
      ? [
          { name: "calls", label: t("reports.metric_calls"), color: "brand.6" },
          { name: "failed_calls", label: t("reports.series_failed"), color: "red.6" },
        ]
      : activeChartMetric === "tokens"
        ? [{ name: "total_tokens", label: t("reports.metric_tokens"), color: "bright-blue.6" }]
        : [{ name: "cost_usd", label: t("reports.metric_cost"), color: "green.6" }]

  const metrics: MetricItem[] = [
    {
      key: "calls",
      label: t("reports.translation_calls"),
      value: formatInt(s?.calls, uiLang),
      delta: pctDelta(s?.calls, s?.prev_calls),
      secondary: s
        ? t("reports.translation_calls_sub", {
            ok: formatInt(s.success_calls, uiLang),
            failed: formatInt(s.failed_calls, uiLang),
          })
        : undefined,
      active: metric === "calls",
      onClick: () => setMetric("calls"),
    },
    {
      key: "success_rate",
      label: t("reports.success_rate"),
      value: formatPercent(s?.success_rate),
      secondary: s ? t("reports.success_rate_sub", { failed: formatInt(s.failed_calls, uiLang) }) : undefined,
    },
    {
      key: "tokens",
      label: t("reports.tokens_total"),
      value: formatInt(s?.total_tokens, uiLang),
      delta: pctDelta(s?.total_tokens, s?.prev_total_tokens),
      secondary: s
        ? t("reports.tokens_sub", {
            prompt: formatInt(s.prompt_tokens, uiLang),
            completion: formatInt(s.completion_tokens, uiLang),
          })
        : undefined,
      active: metric === "tokens",
      onClick: () => setMetric("tokens"),
    },
    {
      key: "cost",
      label: t("reports.cost_and_latency"),
      value: formatUSD(s?.total_cost_usd),
      delta: pctDelta(s?.total_cost_usd, s?.prev_total_cost_usd, true),
      secondary: s
        ? t("reports.cost_sub", {
            ms: formatDuration(s.avg_duration_ms, { unit: "ms" }),
            n: formatInt(s.costed_calls, uiLang),
          })
        : undefined,
      active: metric === "cost",
      onClick: () => setMetric("cost"),
    },
  ]

  if (tab === "agent") {
    const agentMetrics: MetricItem[] = [
      {
        key: "auto_resolve",
        label: t("reports.ai_agent_auto_resolve"),
        description: t("reports.ai_agent_auto_resolve_desc"),
        value: "—",
      },
      {
        key: "handoff_rate",
        label: t("reports.ai_agent_handoff_rate"),
        description: t("reports.ai_agent_handoff_rate_desc"),
        value: "—",
      },
      {
        key: "approvals",
        label: t("reports.ai_agent_approvals"),
        description: t("reports.ai_agent_approvals_desc"),
        value: "—",
      },
      {
        key: "active_agents",
        label: t("reports.ai_agent_active"),
        description: t("reports.ai_agent_active_desc"),
        value: "—",
      },
    ]
    const plannedRows = [
      {
        key: "customer_qa",
        title: t("reports.ai_agent_scope_customer"),
        desc: t("reports.ai_agent_scope_customer_desc"),
      },
      {
        key: "internal_it",
        title: t("reports.ai_agent_scope_internal"),
        desc: t("reports.ai_agent_scope_internal_desc"),
      },
      {
        key: "ticket_draft",
        title: t("reports.ai_agent_scope_ticket"),
        desc: t("reports.ai_agent_scope_ticket_desc"),
      },
    ]
    return (
      <ReportShell title={t("nav.reports_ai")} period={period} toolbarRight={tabSwitcher}>
        <section className="rp-panel">
          <MetricStrip items={agentMetrics} loading={false} />
          <div className="rp-panel-body">
            <Group justify="space-between" align="flex-start" gap="md" wrap="wrap">
              <Group gap="sm" align="flex-start" wrap="nowrap">
                <ThemeIcon size="lg" radius="md" variant="light" color="brand">
                  <IconRobot size={18} stroke={1.6} />
                </ThemeIcon>
                <Stack gap={4}>
                  <Group gap="xs">
                    <Text size="sm" fw={600}>
                      {t("reports.ai_agent_placeholder_title")}
                    </Text>
                    <Badge size="xs" variant="light" color="bright-blue" leftSection={<IconSparkles size={11} />}>
                      {t("reports.ai_agent_planned_badge")}
                    </Badge>
                  </Group>
                  <Text size="xs" c="dimmed" maw={640}>
                    {t("reports.ai_agent_placeholder_desc")}
                  </Text>
                </Stack>
              </Group>
              <Button
                size="xs"
                variant="light"
                rightSection={<IconArrowUpRight size={14} stroke={1.6} />}
                onClick={() => void navigate({ to: "/$slug/ai", params: { slug } })}
              >
                {t("reports.ai_agent_open_hub")}
              </Button>
            </Group>
          </div>
        </section>

        <SectionCard title={t("reports.ai_agent_breakdown_title")} loading={false} empty={false} flush>
          <div className="rp-table-scroll">
            <table className="rp-table">
              <thead>
                <tr>
                  <th>{t("reports.col_scene")}</th>
                  <th data-align="right">{t("reports.ai_agent_auto_resolve")}</th>
                  <th data-align="right">{t("reports.ai_agent_handoff_rate")}</th>
                  <th data-align="right">{t("reports.ai_agent_approvals")}</th>
                </tr>
              </thead>
              <tbody>
                {plannedRows.map((row) => (
                  <tr key={row.key} className="rp-row">
                    <td>
                      <Text size="sm" fw={500}>
                        {row.title}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {row.desc}
                      </Text>
                    </td>
                    <td data-align="right">—</td>
                    <td data-align="right">—</td>
                    <td data-align="right">—</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </SectionCard>
      </ReportShell>
    )
  }

  return (
    <ReportShell
      title={t("nav.reports_ai")}
      period={period}
      meta={data?.period}
      error={query.error}
      updating={pageState.busy}
      toolbarRight={aiEnabled ? tabSwitcher : undefined}
    >
      {/* 主分析面板：点击调用量 / Token / 费用指标格直接切换下方趋势图 */}
      <section className="rp-panel">
        <MetricStrip items={metrics} loading={loading} />
        <div className="rp-panel-body">
          <Group justify="space-between" align="center" gap="xs" mb="sm" wrap="wrap">
            <Group gap="xs" align="baseline">
              <Text size="sm" fw={600}>
                {t("reports.translation_series")}
              </Text>
              <Text size="xs" c="dimmed">
                {t("reports.translation_switch_hint")}
              </Text>
            </Group>
            <GranularityControl period={period} />
          </Group>
          <LoadingStage loading={chartState.busy && !pageState.busy}>
            {loading ? (
              <Skeleton height={240} radius="sm" />
            ) : !chartState.data.series?.length ? (
              <ReportEmpty minHeight={240} />
            ) : (
              <AreaChart
                h={240}
                data={chartState.data.series}
                dataKey="date"
                series={seriesConfig}
                curveType="monotone"
                strokeWidth={2}
                fillOpacity={0.14}
                areaProps={REPORT_AREA_PROPS}
                withLegend
                withDots={false}
                gridAxis="y"
                valueFormatter={(v) => (activeChartMetric === "cost" ? formatUSD(v) : formatInt(v, uiLang))}
              />
            )}
          </LoadingStage>
        </div>
      </section>

      <SectionCard
        title={t("reports.by_scene")}
        loading={loading}
        empty={!data?.by_scene.length}
        minHeight={160}
        flush
      >
        <div className="rp-table-scroll">
          <table className="rp-table">
            <thead>
              <tr>
                <th>{t("reports.col_scene")}</th>
                <th data-align="right">{t("reports.col_calls")}</th>
                <th data-align="right">{t("reports.col_success_rate")}</th>
                <th data-align="right">{t("reports.col_tokens")}</th>
                <th data-align="right">{t("reports.col_avg_duration")}</th>
                <th data-align="right">{t("reports.col_cost")}</th>
              </tr>
            </thead>
            <tbody>
              {(data?.by_scene ?? []).map((row) => (
                <tr key={row.scene} className="rp-row">
                  <td>
                    <Text size="sm" fw={500}>
                      {sceneLabel(row.scene)}
                    </Text>
                  </td>
                  <td data-align="right">
                    <div>{formatInt(row.calls, uiLang)}</div>
                    <InlineShareBar ratio={(row.calls / maxSceneCalls) * 100} />
                  </td>
                  <td data-align="right">
                    <Badge
                      size="sm"
                      variant="light"
                      color={row.failed_calls === 0 ? "green" : (row.success_rate ?? 0) >= 90 ? "yellow" : "red"}
                    >
                      {formatPercent(row.success_rate)}
                    </Badge>
                  </td>
                  <td data-align="right">{formatInt(row.total_tokens, uiLang)}</td>
                  <td data-align="right">{formatDuration(row.avg_duration_ms, { unit: "ms" })}</td>
                  <td data-align="right">{formatUSD(row.cost_usd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </SectionCard>

      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
        <SectionCard
          title={t("reports.by_operation")}
          loading={loading}
          empty={!data?.by_operation.length}
          minHeight={180}
          flush
        >
          <div className="rp-table-scroll">
            <table className="rp-table">
              <thead>
                <tr>
                  <th>{t("reports.col_operation")}</th>
                  <th data-align="right">{t("reports.col_calls")}</th>
                  <th data-align="right">{t("reports.col_success_rate")}</th>
                  <th data-align="right">{t("reports.col_avg_duration")}</th>
                  <th data-align="right">{t("reports.col_cost")}</th>
                </tr>
              </thead>
              <tbody>
                {(data?.by_operation ?? []).map((row) => (
                  <tr key={row.operation} className="rp-row">
                    <td>
                      <Text size="sm" fw={500}>
                        {operationLabel(row.operation)}
                      </Text>
                    </td>
                    <td data-align="right">
                      <div>{formatInt(row.calls, uiLang)}</div>
                      <InlineShareBar ratio={(row.calls / maxOpCalls) * 100} />
                    </td>
                    <td data-align="right">
                      <Badge
                        size="sm"
                        variant="light"
                        color={row.failed_calls === 0 ? "green" : (row.success_rate ?? 0) >= 90 ? "yellow" : "red"}
                      >
                        {formatPercent(row.success_rate)}
                      </Badge>
                    </td>
                    <td data-align="right">{formatDuration(row.avg_duration_ms, { unit: "ms" })}</td>
                    <td data-align="right">{formatUSD(row.cost_usd)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </SectionCard>

        <SectionCard
          title={t("reports.by_model")}
          loading={loading}
          empty={!data?.by_model.length}
          minHeight={180}
          flush
        >
          <div className="rp-table-scroll">
            <table className="rp-table">
              <thead>
                <tr>
                  <th>{t("reports.col_model")}</th>
                  <th data-align="right">{t("reports.col_calls")}</th>
                  <th data-align="right">{t("reports.col_tokens")}</th>
                  <th data-align="right">{t("reports.col_avg_duration")}</th>
                  <th data-align="right">{t("reports.col_cost")}</th>
                </tr>
              </thead>
              <tbody>
                {(data?.by_model ?? []).map((row) => (
                  <tr key={row.model} className="rp-row">
                    <td>
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
                    </td>
                    <td data-align="right">
                      <div>{formatInt(row.calls, uiLang)}</div>
                      <InlineShareBar ratio={(row.calls / maxModelCalls) * 100} />
                    </td>
                    <td data-align="right">{formatInt(row.total_tokens, uiLang)}</td>
                    <td data-align="right">{formatDuration(row.avg_duration_ms, { unit: "ms" })}</td>
                    <td data-align="right">{formatUSD(row.cost_usd)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </SectionCard>

        <SectionCard
          title={t("reports.detected_languages")}
          loading={loading}
          empty={!data?.detected_languages.length}
          minHeight={160}
        >
          <Stack gap={4}>
            {(data?.detected_languages ?? []).map((item) => {
              const unknown = item.language === "und" || !item.language
              const label = unknown ? t("reports.lang_unknown") : languageName(item.language, uiLang)
              const clickable = canInbox && !unknown
              const body = (
                <>
                  <Group justify="space-between" wrap="nowrap">
                    <Text size="sm" c={unknown ? "dimmed" : undefined}>
                      {label}
                    </Text>
                    <Group gap={4} wrap="nowrap">
                      <Text size="xs" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
                        {formatInt(item.count, uiLang)}
                      </Text>
                      {clickable && <IconArrowUpRight size={14} stroke={1.6} className="rp-bar-row-arrow" />}
                    </Group>
                  </Group>
                  <Progress
                    value={(item.count / maxDetected) * 100}
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
                <Tooltip key={item.language} label={t("reports.drill_language_inbox")} openDelay={300}>
                  <Link
                    to="/$slug/inbox"
                    params={{ slug }}
                    search={languageInboxSearch(item.language)}
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
          title={t("reports.reply_languages")}
          loading={loading}
          empty={!data?.reply_languages.length}
          minHeight={160}
        >
          <Stack gap={4}>
            {(data?.reply_languages ?? []).map((item) => (
              <div key={item.language} className="rp-bar-row">
                <Group justify="space-between" wrap="nowrap">
                  <Text size="sm">{languageName(item.language, uiLang)}</Text>
                  <Text size="xs" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
                    {formatInt(item.count, uiLang)}
                  </Text>
                </Group>
                <Progress value={(item.count / maxReply) * 100} size="xs" color="brand" radius="xl" />
              </div>
            ))}
          </Stack>
        </SectionCard>
      </SimpleGrid>
    </ReportShell>
  )
}
