// 报表交互下钻参数构造器：把报表中的实时积压、团队、渠道、标签、语言与成员维度转换为收件箱标准地址参数。

import type { ConversationSort, ConversationStatus } from "@agents-desk/client/tenant"
import { type ConversationFilter, type InboxSearch, serializeFilter } from "../inbox/search"

/** 当前报表已选的公共维度上下文，下钻到收件箱时自动继承。 */
export interface ReportScopeContext {
  audience?: "customer" | "partner" | "internal"
  teamId?: string
  channelId?: string
}

/** 构造带高级筛选条件的收件箱地址参数。 */
function buildInboxSearch(
  filter: ConversationFilter,
  opts?: { status?: ConversationStatus; sort?: ConversationSort },
): InboxSearch {
  const serialized = serializeFilter(filter)
  return {
    box: "all",
    status: opts?.status ?? "open",
    sort: opts?.sort ?? "waiting",
    filter: serialized,
  }
}

/** 将报表当前已选的服务对象/团队/渠道维度并入目标收件箱筛选条件（显式指定时优先生效）。 */
function mergeScope(base: ConversationFilter, scope?: ReportScopeContext): ConversationFilter {
  const next: ConversationFilter = { ...base }
  if (!next.audience && scope?.audience) {
    next.audience = scope.audience
  }
  if (!next.team && scope?.teamId) {
    next.team = { mode: "ids", ids: [scope.teamId] }
  }
  if (!next.channel_ids && scope?.channelId) {
    next.channel_ids = [scope.channelId]
  }
  return next
}

/** 构造实时积压胶囊（打开中 / 等待回复 / 未指派 / 稍后处理）跳转收件箱的参数。 */
export function liveBacklogInboxSearch(
  kind: "open" | "waiting" | "unassigned" | "snoozed",
  scope?: ReportScopeContext,
): InboxSearch {
  const hasScope = Boolean(scope?.audience || scope?.teamId || scope?.channelId)
  if (kind === "open") {
    if (!hasScope) return { box: "all", status: "open", sort: "waiting" }
    return buildInboxSearch(mergeScope({ statuses: ["open"] }, scope))
  }
  if (kind === "snoozed") {
    if (!hasScope) return { box: "all", status: "snoozed", sort: "waiting" }
    return buildInboxSearch(mergeScope({ statuses: ["snoozed"] }, scope), { status: "snoozed" })
  }
  if (kind === "waiting") {
    return buildInboxSearch(mergeScope({ statuses: ["open", "snoozed"], waiting: true }, scope))
  }
  // unassigned_now 统计所有未关闭且无负责人的会话
  return buildInboxSearch(mergeScope({ statuses: ["open", "snoozed"], assignee: { mode: "unassigned" } }, scope))
}

/** 构造按团队收件箱跳转收件箱的参数（teamId 为空时定位未归属团队的打开会话）。 */
export function teamInboxSearch(teamId: string | undefined, scope?: ReportScopeContext): InboxSearch {
  const filter: ConversationFilter = {
    statuses: ["open"],
    team: teamId ? { mode: "ids", ids: [teamId] } : { mode: "unassigned" },
  }
  if (scope?.audience) filter.audience = scope.audience
  if (scope?.channelId) filter.channel_ids = [scope.channelId]
  return buildInboxSearch(filter)
}

/** 构造按渠道跳转收件箱的参数。 */
export function channelInboxSearch(channelId: string, scope?: ReportScopeContext): InboxSearch {
  const filter: ConversationFilter = {
    statuses: ["open"],
    channel_ids: [channelId],
  }
  if (scope?.audience) filter.audience = scope.audience
  if (scope?.teamId) filter.team = { mode: "ids", ids: [scope.teamId] }
  return buildInboxSearch(filter)
}

/** 构造按标签跳转收件箱的参数（含全部状态、按最近活跃排序，便于回溯打标会话）。 */
export function tagInboxSearch(tagId: string, scope?: ReportScopeContext): InboxSearch {
  return buildInboxSearch(
    mergeScope({ statuses: ["open", "snoozed", "closed"], tag_ids: [tagId], tag_match: "any" }, scope),
    { sort: "recent" },
  )
}

/** 构造按客户语言跳转收件箱的参数（含全部状态、按最近活跃排序）。 */
export function languageInboxSearch(language: string, scope?: ReportScopeContext): InboxSearch {
  return buildInboxSearch(
    mergeScope({ statuses: ["open", "snoozed", "closed"], language }, scope),
    { sort: "recent" },
  )
}

/** 构造按成员当前待处理会话跳转收件箱的参数。 */
export function memberOpenInboxSearch(memberId: string, scope?: ReportScopeContext): InboxSearch {
  return buildInboxSearch(
    mergeScope({ statuses: ["open"], assignee: { mode: "member", member_id: memberId } }, scope),
  )
}
