import type {
  ConversationAttributeFilter,
  ConversationInbox,
  ConversationSort,
  ConversationStatus,
  ConversationViewConfig,
} from "@agents-desk/client/tenant"

// 收件箱的地址参数：当前的列表（box，团队收件箱另带 team，视图带 view）、统一筛选条件（filter）、状态、排序、关键词搜索与定位序号。
// 切换会话时保留，返回列表时还在原来的位置。

/** SearchScope 是收件箱内搜索的范围：全部可见会话（默认）或仅当前收件箱列表或视图。 */
export type SearchScope = "all" | "current"

/** MatchField 是搜索结果命中的文本来源字段。 */
export type MatchField = "translated" | "original" | "note"

/** ConversationFilter 是会话的统一筛选条件（不含排序）。 */
export type ConversationFilter = Omit<ConversationViewConfig, "sort">

/** InboxSearch 是收件箱的地址参数。 */
export interface InboxSearch {
  box: ConversationInbox // mine、mentions、unassigned、all；团队收件箱是 all 加上 team
  team?: string
  view?: string // 已保存视图 ID；与 box/team 互斥
  filter?: string // JSON 编码的 ConversationFilter：普通列表高级筛选或在视图内临时微调预览
  tag?: string
  status: ConversationStatus
  sort: ConversationSort
  q?: string // 全文搜索关键词；非空时列表切换为搜索结果模式
  scope?: SearchScope // 搜索范围：all（默认，跨全部状态和可见会话）或 current（限定当前列表或视图）
  seq?: number // 点击搜索结果后定位到时间线中的目标消息序号
  matchField?: MatchField // 命中来源：original 时目标消息自动展开原文
}

const boxes: ConversationInbox[] = ["mine", "mentions", "unassigned", "all"]
const statuses: ConversationStatus[] = ["open", "snoozed", "closed"]
const sorts: ConversationSort[] = ["waiting", "recent"]
const scopes: SearchScope[] = ["all", "current"]
const fields: MatchField[] = ["translated", "original", "note"]

/** pick 返回 value 在 allowed 中时的它，否则是 fallback。 */
const pick = <T extends string>(value: unknown, allowed: T[], fallback: T): T =>
  allowed.includes(value as T) ? (value as T) : fallback

/** validateInboxSearch 是收件箱路由的 validateSearch：不认识的值换成默认值（我的、打开、按等待时长）。 */
export function validateInboxSearch(s: Record<string, unknown>): InboxSearch {
  const view = typeof s.view === "string" && s.view ? s.view : undefined
  const team = !view && typeof s.team === "string" && s.team ? s.team : undefined
  const tag = typeof s.tag === "string" && s.tag ? s.tag : undefined
  const rawFilter = typeof s.filter === "string" && s.filter.trim() ? s.filter.trim() : undefined
  const filter = rawFilter && parseFilter(rawFilter) ? rawFilter : undefined
  const q = typeof s.q === "string" && s.q.trim() ? s.q : undefined
  const scope = q && typeof s.scope === "string" && scopes.includes(s.scope as SearchScope) ? (s.scope as SearchScope) : undefined
  const rawSeq = typeof s.seq === "number" ? s.seq : typeof s.seq === "string" && s.seq ? Number(s.seq) : Number.NaN
  const seq = Number.isInteger(rawSeq) && rawSeq > 0 ? rawSeq : undefined
  const matchField =
    seq && typeof s.matchField === "string" && fields.includes(s.matchField as MatchField) ? (s.matchField as MatchField) : undefined
  return {
    box: view || team ? "all" : pick(s.box, boxes, "mine"),
    team,
    view,
    filter,
    tag,
    status: pick(s.status, statuses, "open"),
    sort: pick(s.sort, sorts, "waiting"),
    q,
    scope,
    seq,
    matchField,
  }
}

/** normalizeFilter 规范化筛选条件：去除空字段、按固定顺序排列状态和无序 ID 数组，保证等价筛选比较与序列化一致。 */
export function normalizeFilter(f: ConversationFilter | undefined): ConversationFilter {
  if (!f) return {}
  const out: ConversationFilter = {}
  if (f.statuses && f.statuses.length > 0) {
    const uniq = statuses.filter((st) => f.statuses?.includes(st))
    if (uniq.length > 0) out.statuses = uniq
  }
  if (f.audience === "customer" || f.audience === "partner" || f.audience === "internal") out.audience = f.audience
  if (f.team) {
    if (f.team.mode === "ids") {
      const ids = [...new Set((f.team.ids ?? []).filter(Boolean))].sort()
      if (ids.length > 0) out.team = { mode: "ids", ids }
    } else if (f.team.mode === "my_teams" || f.team.mode === "unassigned") {
      out.team = { mode: f.team.mode }
    }
  }
  if (f.assignee) {
    if (f.assignee.mode === "member") {
      if (f.assignee.member_id) out.assignee = { mode: "member", member_id: f.assignee.member_id }
    } else if (f.assignee.mode === "me" || f.assignee.mode === "unassigned") {
      out.assignee = { mode: f.assignee.mode }
    }
  }
  if (typeof f.waiting === "boolean") out.waiting = f.waiting
  if (f.priority === "high" || f.priority === "normal") out.priority = f.priority
  if (f.channel_ids && f.channel_ids.length > 0) {
    const ids = [...new Set(f.channel_ids.filter(Boolean))].sort()
    if (ids.length > 0) out.channel_ids = ids
  }
  if (f.tag_ids && f.tag_ids.length > 0) {
    const ids = [...new Set(f.tag_ids.filter(Boolean))].sort()
    if (ids.length > 0) {
      out.tag_ids = ids
      out.tag_match = f.tag_match === "all" ? "all" : "any"
    }
  }
  const lang = f.language?.trim()
  if (lang) out.language = lang
  if (f.attributes && f.attributes.length > 0) {
    const attrs: ConversationAttributeFilter[] = []
    for (const a of f.attributes) {
      if (!a.key || !a.op) continue
      if (a.op === "is_set" || a.op === "is_empty") {
        attrs.push({ key: a.key, op: a.op })
      } else if (Array.isArray(a.value)) {
        const vals = [...new Set(a.value.map(String).filter(Boolean))].sort()
        if (vals.length > 0) attrs.push({ key: a.key, op: a.op, value: vals })
      } else if (a.value !== undefined && a.value !== null && a.value !== "") {
        attrs.push({ key: a.key, op: a.op, value: a.value })
      }
    }
    if (attrs.length > 0) out.attributes = attrs
  }
  return out
}

/** normalizeViewConfig 规范化完整的视图配置（筛选条件 + 默认排序），供 useSavedView 比较脏状态。 */
export function normalizeViewConfig(cfg: ConversationViewConfig | undefined): ConversationViewConfig {
  return {
    ...normalizeFilter(cfg),
    sort: cfg?.sort === "recent" ? "recent" : "waiting",
  }
}

/** parseFilter 解析地址栏中的 JSON 筛选参数，非法 JSON 返回 undefined。 */
export function parseFilter(raw: string | undefined): ConversationFilter | undefined {
  if (!raw) return undefined
  try {
    const v = JSON.parse(raw) as unknown
    if (!v || typeof v !== "object" || Array.isArray(v)) return undefined
    return normalizeFilter(v as ConversationFilter)
  } catch {
    return undefined
  }
}

/** serializeFilter 将筛选条件规范化并序列化为地址参数字符串；没有任何条件时返回 undefined。 */
export function serializeFilter(f: ConversationFilter | undefined): string | undefined {
  const norm = normalizeFilter(f)
  return Object.keys(norm).length > 0 ? JSON.stringify(norm) : undefined
}

/** countFilterCriteria 返回筛选条件中包含的维度条数，用于筛选按钮上的徽标提示。 */
export function countFilterCriteria(f: ConversationFilter | undefined): number {
  const n = normalizeFilter(f)
  let count = 0
  if (n.statuses && n.statuses.length > 0) count++
  if (n.audience) count++
  if (n.team) count++
  if (n.assignee) count++
  if (typeof n.waiting === "boolean") count++
  if (n.priority) count++
  if (n.channel_ids && n.channel_ids.length > 0) count++
  if (n.tag_ids && n.tag_ids.length > 0) count++
  if (n.language) count++
  if (n.attributes) count += n.attributes.length
  return count
}

/** filterFromSearch 将当前普通收件箱（非视图模式）的条件转换为同构的 ConversationViewConfig，用于“保存为视图”。 */
export function filterFromSearch(s: InboxSearch): ConversationViewConfig {
  const parsed = parseFilter(s.filter)
  if (parsed) {
    return normalizeViewConfig({ ...parsed, sort: s.sort })
  }
  const f: ConversationFilter = {
    statuses: s.box === "mentions" ? undefined : [s.status],
  }
  if (s.team) {
    f.team = { mode: "ids", ids: [s.team] }
  } else if (s.box === "mine") {
    f.assignee = { mode: "me" }
  } else if (s.box === "unassigned") {
    f.team = { mode: "unassigned" }
    f.assignee = { mode: "unassigned" }
  }
  if (s.tag) {
    f.tag_ids = [s.tag]
    f.tag_match = "any"
  }
  return normalizeViewConfig({ ...f, sort: s.sort })
}
