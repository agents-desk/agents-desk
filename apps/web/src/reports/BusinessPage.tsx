// 商务沟通报表：聚焦对外商务渠道（个人网页商务链接与 Telegram 个人号）的活跃度、收发消息量与商务成员拆解。

import type { MemberStatus } from "@agents-desk/client/tenant"
import { AreaChart } from "@mantine/charts"
import { Avatar, Badge, Button, Center, Group, Progress, Select, SimpleGrid, Skeleton, Stack, Text } from "@mantine/core"
import { IconX } from "@tabler/icons-react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { LoadingStage, type PeriodRoute, useLanguage, usePeriod, useSmoothTransition, useT } from "@go-tenon/ui"
import { type ReactNode, useCallback } from "react"
import { kit } from "../api"
import { languageName } from "../languages"
import { StatusBadge } from "../status"
import { InlineShareBar, REPORT_AREA_PROPS } from "./Breakdown"
import { formatInt, pctDelta } from "./format"
import { type MetricItem, MetricStrip } from "./MetricStrip"
import { GranularityControl } from "./PeriodButton"
import { ReportShell } from "./ReportShell"
import type { BusinessReportSearchParams } from "./search"
import { SectionCard } from "./SectionCard"

/** 商务沟通报表页面组件。 */
export function BusinessPage({ route }: { route: PeriodRoute }): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const navigate = useNavigate()
  const period = usePeriod(route, { defaultPreset: "7d" })
  const search = route.useSearch() as BusinessReportSearchParams
  const channelKind = search.channel_kind
  const ownerId = search.owner_id

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

  const members = useQuery(kit.queries.member.list({ per_page: 100 }))
  const queryParams = {
    ...period.params,
    ...(channelKind ? { channel_kind: channelKind } : {}),
    ...(ownerId ? { owner_id: ownerId } : {}),
  }
  const query = useQuery({
    ...kit.queries.report.business(queryParams),
    placeholderData: keepPreviousData,
  })

  const scopeKey = `${queryParams.from}|${queryParams.to}|${queryParams.tz ?? ""}|${channelKind ?? ""}|${ownerId ?? ""}`
  const granKey = `${scopeKey}|${queryParams.granularity ?? ""}`
  const pageState = useSmoothTransition(query.data, scopeKey, query.isFetching)
  const chartState = useSmoothTransition(query.data?.series, granKey, query.isFetching)
  const data = pageState.data
  const loading = query.isPending && !data
  const s = data?.summary

  const hasFilters = Boolean(channelKind || ownerId)
  const kindLabel = (kind: string): string => {
    if (kind === "business_link") return t("reports.biz_kind_link")
    if (kind === "telegram_account") return t("reports.biz_kind_tg_account")
    return kind
  }

  const toolbarExtra = (
    <>
      <Select
        size="sm"
        w={172}
        clearable
        placeholder={t("reports.all_biz_channels")}
        value={channelKind ?? null}
        onChange={(v) => patchSearch({ channel_kind: v ?? undefined })}
        data={[
          { value: "business_link", label: t("reports.biz_kind_link") },
          { value: "telegram_account", label: t("reports.biz_kind_tg_account") },
        ]}
      />
      <Select
        size="sm"
        w={164}
        clearable
        searchable
        placeholder={t("reports.all_owners")}
        value={ownerId ?? null}
        onChange={(v) => patchSearch({ owner_id: v ?? undefined })}
        data={(members.data?.items ?? []).map((m) => ({ value: m.id, label: m.name || m.email }))}
      />
      {hasFilters && (
        <Button
          size="sm"
          variant="subtle"
          color="gray"
          leftSection={<IconX size={14} stroke={1.6} />}
          onClick={() => patchSearch({ channel_kind: undefined, owner_id: undefined })}
        >
          {t("reports.clear_filters")}
        </Button>
      )}
    </>
  )

  const metrics: MetricItem[] = [
    {
      key: "active_chats",
      label: t("reports.biz_active_chats"),
      value: formatInt(s?.active_chats, uiLang),
      delta: pctDelta(s?.active_chats, s?.prev_active_chats),
      secondary: s
        ? t("reports.biz_mode_sub", {
            direct: formatInt(s.direct_chats, uiLang),
            group: formatInt(s.group_chats, uiLang),
          })
        : undefined,
    },
    {
      key: "new_chats",
      label: t("reports.biz_new_chats"),
      value: formatInt(s?.new_chats, uiLang),
      delta: pctDelta(s?.new_chats, s?.prev_new_chats),
    },
    {
      key: "messages",
      label: t("reports.biz_messages"),
      value: formatInt(s?.messages_total, uiLang),
      delta: pctDelta(s?.messages_total, s?.prev_messages_total),
      secondary: s
        ? t("reports.biz_msg_dir_sub", {
            in: formatInt(s.messages_inbound, uiLang),
            out: formatInt(s.messages_outbound, uiLang),
          })
        : undefined,
    },
    {
      key: "waiting",
      label: t("reports.biz_waiting_now"),
      description: t("reports.biz_waiting_now_desc"),
      value: formatInt(s?.waiting_now, uiLang),
      secondary: s
        ? t("reports.biz_live_sub", {
            chats: formatInt(s.total_chats_now, uiLang),
            channels: formatInt(s.active_channels_now, uiLang),
          })
        : undefined,
    },
  ]

  const maxKindMsgs = Math.max(1, ...(data?.by_channel_kind ?? []).map((x) => x.messages_total))
  const maxOwnerMsgs = Math.max(1, ...(data?.by_owner ?? []).map((x) => x.messages_inbound + x.messages_outbound))
  const maxLang = Math.max(1, ...(data?.by_language ?? []).map((x) => x.count))

  return (
    <ReportShell
      title={t("nav.reports_business")}
      period={period}
      toolbarExtra={toolbarExtra}
      meta={data?.period}
      error={query.error}
      updating={pageState.busy}
    >
      <section className="rp-panel">
        <MetricStrip items={metrics} loading={loading} />
        <div className="rp-panel-body">
          <Group justify="space-between" align="center" gap="xs" mb="sm" wrap="wrap">
            <Text size="sm" fw={600}>
              {t("reports.biz_series_title")}
            </Text>
            <GranularityControl period={period} />
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
                  { name: "inbound", label: t("reports.biz_series_inbound"), color: "bright-blue.6" },
                  { name: "outbound", label: t("reports.biz_series_outbound"), color: "brand.6" },
                  { name: "new_chats", label: t("reports.biz_new_chats"), color: "green.6" },
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
          title={t("reports.biz_by_channel_kind")}
          loading={loading}
          empty={!data?.by_channel_kind.length}
          minHeight={180}
          flush
        >
          <table className="rp-table">
            <thead>
              <tr>
                <th>{t("reports.col_kind")}</th>
                <th data-align="right">{t("reports.biz_active_chats")}</th>
                <th data-align="right">{t("reports.biz_new_chats")}</th>
                <th data-align="right">{t("reports.biz_messages")}</th>
              </tr>
            </thead>
            <tbody>
              {(data?.by_channel_kind ?? []).map((row) => (
                <tr
                  key={row.kind}
                  className="rp-row"
                  data-clickable=""
                  data-selected={channelKind === row.kind ? "" : undefined}
                  onClick={() => patchSearch({ channel_kind: channelKind === row.kind ? undefined : row.kind })}
                >
                  <td>
                    <Text size="sm" fw={500}>
                      {kindLabel(row.kind)}
                    </Text>
                  </td>
                  <td data-align="right">{formatInt(row.active_chats, uiLang)}</td>
                  <td data-align="right">{formatInt(row.new_chats, uiLang)}</td>
                  <td data-align="right">
                    <div>{formatInt(row.messages_total, uiLang)}</div>
                    <InlineShareBar ratio={(row.messages_total / maxKindMsgs) * 100} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </SectionCard>

        <SectionCard
          title={t("reports.biz_by_language")}
          loading={loading}
          empty={!data?.by_language.length}
          minHeight={180}
        >
          <Stack gap={4}>
            {(data?.by_language ?? []).map((item) => {
              const unknown = item.language === "und" || !item.language
              return (
                <div key={item.language || "__unknown__"} className="rp-bar-row">
                  <Group justify="space-between" wrap="nowrap">
                    <Text size="sm" c={unknown ? "dimmed" : undefined}>
                      {unknown ? t("reports.lang_unknown") : languageName(item.language, uiLang)}
                    </Text>
                    <Text size="xs" fw={500} style={{ fontVariantNumeric: "tabular-nums" }}>
                      {formatInt(item.count, uiLang)}
                    </Text>
                  </Group>
                  <Progress
                    value={(item.count / maxLang) * 100}
                    size="xs"
                    color={unknown ? "gray" : "bright-blue"}
                    radius="xl"
                  />
                </div>
              )
            })}
          </Stack>
        </SectionCard>
      </SimpleGrid>

      <SectionCard
        title={t("reports.biz_by_owner")}
        loading={loading}
        empty={!data?.by_owner.length}
        minHeight={180}
        flush
      >
        <table className="rp-table">
          <thead>
            <tr>
              <th>{t("reports.col_member")}</th>
              <th data-align="right">{t("reports.biz_col_channels")}</th>
              <th data-align="right">{t("reports.biz_active_chats")}</th>
              <th data-align="right">{t("reports.biz_new_chats")}</th>
              <th data-align="right">{t("reports.biz_col_inbound")}</th>
              <th data-align="right">{t("reports.biz_col_outbound")}</th>
            </tr>
          </thead>
          <tbody>
            {(data?.by_owner ?? []).map((row) => {
              const total = row.messages_inbound + row.messages_outbound
              return (
                <tr
                  key={row.member_id}
                  className="rp-row"
                  data-clickable=""
                  data-selected={ownerId === row.member_id ? "" : undefined}
                  onClick={() => patchSearch({ owner_id: ownerId === row.member_id ? undefined : row.member_id })}
                >
                  <td>
                    <Group gap="xs" wrap="nowrap">
                      <Avatar src={row.avatar_url || undefined} size="xs" radius="xl" color="brand">
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
                  </td>
                  <td data-align="right">{formatInt(row.channels_count, uiLang)}</td>
                  <td data-align="right">{formatInt(row.active_chats, uiLang)}</td>
                  <td data-align="right">{formatInt(row.new_chats, uiLang)}</td>
                  <td data-align="right">{formatInt(row.messages_inbound, uiLang)}</td>
                  <td data-align="right">
                    <div>{formatInt(row.messages_outbound, uiLang)}</div>
                    <InlineShareBar ratio={(total / maxOwnerMsgs) * 100} />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </SectionCard>
    </ReportShell>
  )
}
