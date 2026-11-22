import type { ConversationInbox, ConversationSort, ConversationStatus } from "@agents-desk/client/tenant"

// 收件箱的地址参数：当前的列表（box，团队收件箱另带 team）、状态、排序、关键词搜索与定位序号。
// 切换会话时保留，返回列表时还在原来的位置。

/** SearchScope 是收件箱内搜索的范围：全部可见会话（默认）或仅当前收件箱列表。 */
export type SearchScope = "all" | "current"

/** MatchField 是搜索结果命中的文本来源字段。 */
export type MatchField = "translated" | "original" | "note"

/** InboxSearch 是收件箱的地址参数。 */
export interface InboxSearch {
  box: ConversationInbox // mine、mentions、unassigned、all；团队收件箱是 all 加上 team
  team?: string
  tag?: string
  status: ConversationStatus
  sort: ConversationSort
  q?: string // 全文搜索关键词；非空时列表切换为搜索结果模式
  scope?: SearchScope // 搜索范围：all（默认，跨全部状态和可见会话）或 current（限定当前列表）
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
  const team = typeof s.team === "string" && s.team ? s.team : undefined
  const tag = typeof s.tag === "string" && s.tag ? s.tag : undefined
  const q = typeof s.q === "string" && s.q.trim() ? s.q : undefined
  const scope = q && typeof s.scope === "string" && scopes.includes(s.scope as SearchScope) ? (s.scope as SearchScope) : undefined
  const rawSeq = typeof s.seq === "number" ? s.seq : typeof s.seq === "string" && s.seq ? Number(s.seq) : Number.NaN
  const seq = Number.isInteger(rawSeq) && rawSeq > 0 ? rawSeq : undefined
  const matchField =
    seq && typeof s.matchField === "string" && fields.includes(s.matchField as MatchField) ? (s.matchField as MatchField) : undefined
  return {
    box: team ? "all" : pick(s.box, boxes, "mine"),
    team,
    tag,
    status: pick(s.status, statuses, "open"),
    sort: pick(s.sort, sorts, "waiting"),
    q,
    scope,
    seq,
    matchField,
  }
}
