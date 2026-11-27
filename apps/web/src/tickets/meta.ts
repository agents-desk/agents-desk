import type { ConversationTicketDTO, TicketStatus, TicketTypeVisibility } from "@agents-desk/client/tenant"
import { infiniteQueryOptions, type QueryClient, queryOptions, useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query"
import type { useT } from "@go-tenon/ui"
import { kit, tenantApi } from "../api"
import { timelineQuery } from "../inbox/data"
import { findInLists } from "../preload"
import type { TicketSearch } from "./search"
import { prefetchQueries, type PreloadQuery } from "@go-tenon/ui"

type T = ReturnType<typeof useT>

/** TICKET_STATUSES 是工单全部 4 种状态的固定顺序。 */
export const TICKET_STATUSES: TicketStatus[] = ["submitted", "in_progress", "waiting_on_customer", "resolved"]

/** ticketStatusColor 是工单 4 种状态对应的 Mantine 语义预设色。 */
export const ticketStatusColor: Record<TicketStatus, string> = {
  submitted: "gray",
  in_progress: "bright-blue",
  waiting_on_customer: "yellow",
  resolved: "green",
}

/** ticketStatusLabel 返回工单状态在当前界面语言下的展示文案。 */
export function ticketStatusLabel(status: TicketStatus, t: T): string {
  switch (status) {
    case "submitted":
      return t("tickets.status_submitted")
    case "in_progress":
      return t("tickets.status_in_progress")
    case "waiting_on_customer":
      return t("tickets.status_waiting_on_customer")
    case "resolved":
      return t("tickets.status_resolved")
  }
}

/** ticketVisibilityLabel 返回工单可见范围在当前界面语言下的展示文案。 */
export function ticketVisibilityLabel(visibility: TicketTypeVisibility, t: T): string {
  return visibility === "all" ? t("tickets.visibility_all") : t("tickets.visibility_members")
}

/** useTicketTypes 读取工作区的工单类型列表；enabledOnly 为 true 时仅返回未停用的类型。 */
export function useTicketTypes(enabledOnly = false) {
  const q = useQuery({
    ...kit.queries.ticket.typeList({ enabled_only: enabledOnly || undefined }),
    staleTime: 60_000,
  })
  return q.data ?? []
}

/** useTicketAttributes 读取指定工单类型下的自定义字段定义。 */
export function useTicketAttributes(ticketTypeId: string | undefined) {
  const q = useQuery({
    ...kit.queries.attribute.list({ target: "ticket", ticket_type_id: ticketTypeId }),
    enabled: Boolean(ticketTypeId),
    staleTime: 60_000,
  })
  return q.data ?? []
}

/** useTicketList 按当前筛选与分组游标分页读取工单列表。 */
export function useTicketList(s: TicketSearch) {
  return useInfiniteQuery(ticketListQuery(s))
}

/** ticketListQuery 是工单列表的查询，游标分页（useTicketList 与路由 loader 共用）。 */
export function ticketListQuery(s: TicketSearch) {
  return infiniteQueryOptions({
    queryKey: [...kit.queries.ticket.key(), "list", s.box, s.status, s.type ?? "", s.q ?? ""],
    queryFn: ({ pageParam }) =>
      tenantApi.ticket.list({
        inbox: s.box,
        status: s.status !== "unresolved" && s.status !== "all" ? s.status : undefined,
        unresolved_only: s.status === "unresolved" ? true : undefined,
        type_id: s.type,
        q: s.q?.trim() || undefined,
        cursor: pageParam || undefined,
        per_page: 50,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor,
  })
}

/** flattenTickets 把工单游标分页结果拍平成数组。 */
export const flattenTickets = (pages: { items: ConversationTicketDTO[] }[] | undefined) => (pages ?? []).flatMap((p) => p.items)

/** useTicket 读取单张工单详情（含当前成员可见的关联会话列表）。 */
export function useTicket(id: string) {
  const queryClient = useQueryClient()
  return useQuery({
    ...kit.queries.ticket.get(id),
    enabled: Boolean(id),
    placeholderData: () => findInLists<ConversationTicketDTO>(queryClient, [...kit.queries.ticket.key(), "list"], id),
  })
}

/** ticketQueries 是打开工单 id 要用的查询：工单和时间线（路由 loader 与预取共用）。 */
export function ticketQueries(queryClient: QueryClient, id: string): PreloadQuery[] {
  return [kit.queries.ticket.get(id), timelineQuery(queryClient, id)]
}

/** prefetchTicket 预取工单 id 及其时间线：列表行悬停、J/K 切换用。 */
export function prefetchTicket(queryClient: QueryClient, id: string): Promise<void> {
  return prefetchQueries(queryClient, ticketQueries(queryClient, id))
}

/** useConversationTickets 读取指定服务型会话关联的全部可见工单。 */
export function useConversationTickets(conversationId: string | undefined) {
  return useQuery({
    queryKey: [...kit.queries.ticket.key(), "by-conversation", conversationId],
    queryFn: () => tenantApi.ticket.list({ conversation_id: conversationId, per_page: 50 }),
    enabled: Boolean(conversationId),
  })
}

/** useContactTickets 读取指定联系人关联的全部可见工单。 */
export function useContactTickets(contactId: string | undefined) {
  return useQuery({ ...contactTicketsQuery(contactId ?? ""), enabled: Boolean(contactId) })
}

/** contactTicketsQuery 是联系人 contactId 关联工单的查询（档案与预取共用）。 */
export function contactTicketsQuery(contactId: string) {
  return queryOptions({
    queryKey: [...kit.queries.ticket.key(), "by-contact", contactId],
    queryFn: () => tenantApi.ticket.list({ contact_id: contactId, per_page: 30 }),
  })
}
