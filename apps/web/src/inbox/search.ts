import type { ConversationInbox, ConversationSort, ConversationStatus } from "@agents-desk/client/tenant"

// 收件箱的地址参数：当前的列表（box，团队收件箱另带 team）、状态和排序。切换会话时保留，返回列表时还在原来的位置。

/** InboxSearch 是收件箱的地址参数。 */
export interface InboxSearch {
  box: ConversationInbox // mine、mentions、unassigned、all；团队收件箱是 all 加上 team
  team?: string
  tag?: string
  status: ConversationStatus
  sort: ConversationSort
}

const boxes: ConversationInbox[] = ["mine", "mentions", "unassigned", "all"]
const statuses: ConversationStatus[] = ["open", "snoozed", "closed"]
const sorts: ConversationSort[] = ["waiting", "recent"]

/** pick 返回 value 在 allowed 中时的它，否则是 fallback。 */
const pick = <T extends string>(value: unknown, allowed: T[], fallback: T): T =>
  allowed.includes(value as T) ? (value as T) : fallback

/** validateInboxSearch 是收件箱路由的 validateSearch：不认识的值换成默认值（我的、打开、按等待时长）。 */
export function validateInboxSearch(s: Record<string, unknown>): InboxSearch {
  const team = typeof s.team === "string" && s.team ? s.team : undefined
  const tag = typeof s.tag === "string" && s.tag ? s.tag : undefined
  return {
    box: team ? "all" : pick(s.box, boxes, "mine"),
    team,
    tag,
    status: pick(s.status, statuses, "open"),
    sort: pick(s.sort, sorts, "waiting"),
  }
}
