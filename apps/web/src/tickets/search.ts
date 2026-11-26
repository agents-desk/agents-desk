import type { TicketStatus } from "@agents-desk/client/tenant"

// 工单列表的地址参数：当前分组（mine / unassigned / all）、状态筛选（unresolved / all / 4 种具体状态）、工单类型和关键词搜索。

/** TicketBox 是工单侧栏的三个内置分组。 */
export type TicketBox = "mine" | "unassigned" | "all"

/** TicketStatusFilter 是工单列表的状态筛选项。 */
export type TicketStatusFilter = "unresolved" | "all" | TicketStatus

/** TicketSearch 是工单页的地址参数。 */
export interface TicketSearch {
  box: TicketBox
  status: TicketStatusFilter
  type?: string
  q?: string
}

const boxes: TicketBox[] = ["mine", "unassigned", "all"]
const statuses: TicketStatusFilter[] = ["unresolved", "all", "submitted", "in_progress", "waiting_on_customer", "resolved"]

/** pick 返回 value 在 allowed 中时的值，否则回落到 fallback。 */
const pick = <T extends string>(value: unknown, allowed: T[], fallback: T): T =>
  allowed.includes(value as T) ? (value as T) : fallback

/** validateTicketSearch 是工单路由的 validateSearch：非法参数自动回落到默认值（我的工单、未解决）。 */
export function validateTicketSearch(s: Record<string, unknown>): TicketSearch {
  const type = typeof s.type === "string" && s.type.trim() ? s.type.trim() : undefined
  const q = typeof s.q === "string" && s.q.trim() ? s.q : undefined
  return {
    box: pick(s.box, boxes, "mine"),
    status: pick(s.status, statuses, "unresolved"),
    type,
    q,
  }
}
