// 报表路由的地址查询参数解析与同步：组合 @go-tenon/ui 的 periodSearch / usePeriod，
// 并支持按团队收件箱（team_id）和渠道（channel_id）筛选，以及满意度评价明细的独立分页和星级筛选。

import {
  type PeriodQueryParams,
  type PeriodRoute,
  type PeriodSearchOptions,
  type PeriodSearchParams,
  type PeriodState,
  periodSearch,
  usePeriod,
} from "@go-tenon/ui"
import { useNavigate } from "@tanstack/react-router"
import { useCallback, useMemo } from "react"

/** 保存在 URL search 中的报表通用筛选参数（时间窗口 + 团队 + 渠道）。 */
export interface ReportFilterSearchParams extends PeriodSearchParams {
  team_id?: string
  channel_id?: string
}

/** 传给支持团队与渠道筛选的后端报表接口的查询参数。 */
export interface ReportFilterQueryParams extends PeriodQueryParams {
  audience?: "customer" | "partner" | "internal"
  team_id?: string
  channel_id?: string
}

/** 满意度报表页面的 URL search 参数：在通用筛选外，增加评价明细表的分页和星级筛选。 */
export interface SatisfactionSearchParams extends ReportFilterSearchParams {
  tab?: "volume" | "response" | "satisfaction"
  page?: number
  per_page?: number
  min_score?: number
  max_score?: number
}

/** 客服报表的 URL search 参数（统一承接会话量、响应时效、满意度 3 个 Tab）。 */
export type ServiceReportSearchParams = SatisfactionSearchParams

/** 内部服务报表的 URL search 参数（统一承接请求量、响应时效 2 个 Tab）。 */
export interface InternalServiceReportSearchParams extends ReportFilterSearchParams {
  tab?: "volume" | "response"
}

/** AI 与翻译报表的 URL search 参数（统一承接翻译用量、AI Agent 效能 2 个 Tab）。 */
export interface TranslationReportSearchParams extends PeriodSearchParams {
  tab?: "translation" | "agent"
}

/** 工单报表的 URL search 参数。 */
export interface TicketsReportSearchParams extends PeriodSearchParams {
  team_id?: string
  type_id?: string
  visibility?: "all" | "members"
}

/** 商务报表的 URL search 参数（统一承接商务沟通、服务请求、服务响应 3 个 Tab）。 */
export interface BusinessReportSearchParams extends ReportFilterSearchParams {
  tab?: "chat" | "volume" | "response"
  channel_kind?: "business_link" | "telegram_account"
  owner_id?: string
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** 校验并解析通用报表查询参数（时间窗口 + team_id + channel_id），用于路由的 validateSearch。 */
export function reportFilterSearch(
  options: PeriodSearchOptions = { defaultPreset: "7d" },
): (search: Record<string, unknown>) => ReportFilterSearchParams {
  const parsePeriod = periodSearch(options)
  return (search: Record<string, unknown>): ReportFilterSearchParams => {
    const out: ReportFilterSearchParams = parsePeriod(search)
    if (typeof search.team_id === "string" && UUID_RE.test(search.team_id)) {
      out.team_id = search.team_id
    }
    if (typeof search.channel_id === "string" && UUID_RE.test(search.channel_id)) {
      out.channel_id = search.channel_id
    }
    return out
  }
}

/** 校验并解析客服报表的查询参数（Tab + 通用筛选 + 评价明细分页和星级范围）。 */
export function serviceReportSearch(
  options: PeriodSearchOptions = { defaultPreset: "7d" },
): (search: Record<string, unknown>) => ServiceReportSearchParams {
  const parseBase = reportFilterSearch(options)
  return (search: Record<string, unknown>): ServiceReportSearchParams => {
    const out: ServiceReportSearchParams = parseBase(search)
    if (search.tab === "volume" || search.tab === "response" || search.tab === "satisfaction") {
      out.tab = search.tab
    }
    const page = Number(search.page)
    if (Number.isInteger(page) && page > 1) out.page = page
    const perPage = Number(search.per_page)
    if (Number.isInteger(perPage) && perPage >= 1 && perPage <= 100 && perPage !== 20) out.per_page = perPage
    const minScore = Number(search.min_score)
    if (Number.isInteger(minScore) && minScore >= 1 && minScore <= 5) out.min_score = minScore
    const maxScore = Number(search.max_score)
    if (Number.isInteger(maxScore) && maxScore >= 1 && maxScore <= 5) out.max_score = maxScore
    return out
  }
}

/** 校验并解析内部服务报表的查询参数（Tab + 通用筛选）。 */
export function internalServiceReportSearch(
  options: PeriodSearchOptions = { defaultPreset: "7d" },
): (search: Record<string, unknown>) => InternalServiceReportSearchParams {
  const parseBase = reportFilterSearch(options)
  return (search: Record<string, unknown>): InternalServiceReportSearchParams => {
    const out: InternalServiceReportSearchParams = parseBase(search)
    if (search.tab === "volume" || search.tab === "response") {
      out.tab = search.tab
    }
    return out
  }
}

/** 校验并解析 AI 与翻译报表的查询参数（时间窗口 + Tab）。 */
export function translationReportSearch(
  options: PeriodSearchOptions = { defaultPreset: "7d" },
): (search: Record<string, unknown>) => TranslationReportSearchParams {
  const parsePeriod = periodSearch(options)
  return (search: Record<string, unknown>): TranslationReportSearchParams => {
    const out: TranslationReportSearchParams = parsePeriod(search)
    if (search.tab === "translation" || search.tab === "agent") {
      out.tab = search.tab
    }
    return out
  }
}

/** 校验并解析工单报表的查询参数。 */
export function ticketsReportSearch(
  options: PeriodSearchOptions = { defaultPreset: "7d" },
): (search: Record<string, unknown>) => TicketsReportSearchParams {
  const parsePeriod = periodSearch(options)
  return (search: Record<string, unknown>): TicketsReportSearchParams => {
    const out: TicketsReportSearchParams = parsePeriod(search)
    if (typeof search.team_id === "string" && UUID_RE.test(search.team_id)) out.team_id = search.team_id
    if (typeof search.type_id === "string" && UUID_RE.test(search.type_id)) out.type_id = search.type_id
    if (search.visibility === "all" || search.visibility === "members") out.visibility = search.visibility
    return out
  }
}

/** 校验并解析商务报表的查询参数（Tab + 通用筛选 + 商务渠道与成员筛选）。 */
export function businessReportSearch(
  options: PeriodSearchOptions = { defaultPreset: "7d" },
): (search: Record<string, unknown>) => BusinessReportSearchParams {
  const parseBase = reportFilterSearch(options)
  return (search: Record<string, unknown>): BusinessReportSearchParams => {
    const out: BusinessReportSearchParams = parseBase(search)
    if (search.tab === "chat" || search.tab === "volume" || search.tab === "response") {
      out.tab = search.tab
    }
    if (search.channel_kind === "business_link" || search.channel_kind === "telegram_account") {
      out.channel_kind = search.channel_kind
    }
    if (typeof search.owner_id === "string" && UUID_RE.test(search.owner_id)) out.owner_id = search.owner_id
    return out
  }
}

/** useReportFilters 返回的报表筛选状态与操作。 */
export interface ReportFiltersState {
  period: PeriodState
  audience?: "customer" | "partner" | "internal"
  teamId: string | undefined
  channelId: string | undefined
  setTeamId(id: string | undefined): void
  setChannelId(id: string | undefined): void
  clearDimensions(): void
  /** 直接传给支持 audience / team_id / channel_id 的报表接口的参数对象。 */
  queryParams: ReportFilterQueryParams
}

/** 读取并同步地址中的时间窗口、团队收件箱与渠道筛选条件；改变维度时自动把明细分页归回第 1 页。 */
export function useReportFilters(
  route: PeriodRoute,
  options: {
    defaultPreset?: "today" | "7d" | "30d" | "this_month" | "last_month"
    audience?: "customer" | "partner" | "internal"
  } = { defaultPreset: "7d" },
): ReportFiltersState {
  const period = usePeriod(route, options)
  const search = route.useSearch() as ReportFilterSearchParams
  const navigate = useNavigate()
  const audience = options.audience

  const teamId = typeof search.team_id === "string" && search.team_id ? search.team_id : undefined
  const channelId = typeof search.channel_id === "string" && search.channel_id ? search.channel_id : undefined

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
          delete next.page
          return next
        }) as never,
        replace: true,
      })
    },
    [navigate],
  )

  const setTeamId = useCallback((id: string | undefined) => patchSearch({ team_id: id }), [patchSearch])
  const setChannelId = useCallback((id: string | undefined) => patchSearch({ channel_id: id }), [patchSearch])
  const clearDimensions = useCallback(() => patchSearch({ team_id: undefined, channel_id: undefined }), [patchSearch])

  const queryParams = useMemo<ReportFilterQueryParams>(() => {
    const out: ReportFilterQueryParams = { ...period.params }
    if (audience) out.audience = audience
    if (teamId) out.team_id = teamId
    if (channelId) out.channel_id = channelId
    return out
  }, [period.params, audience, teamId, channelId])

  return { period, audience, teamId, channelId, setTeamId, setChannelId, clearDimensions, queryParams }
}
