import type { ChatMode, ChatScope, ConversationChatDTO } from "@agents-desk/client/tenant"
import { infiniteQueryOptions, type QueryClient, useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query"
import type { useT } from "@go-tenon/ui"
import { kit, tenantApi } from "../api"
import { timelineQuery } from "../inbox/data"
import { findInLists, prefetch, type Warmable } from "../preload"

type T = ReturnType<typeof useT>

/** ChatModeFilter 是聊天列表的模式筛选：全部、仅单聊或仅群聊。 */
export type ChatModeFilter = "all" | ChatMode

/** useChatList 按范围（内部聊天/商务沟通）、模式、渠道与搜索词拉取当前成员参与的聊天列表（游标无限滚动）。 */
export function useChatList(q: string, mode: ChatModeFilter, scope: ChatScope, channelId?: string) {
  return useInfiniteQuery(chatListQuery(q, mode, scope, channelId))
}

/** chatListQuery 是聊天列表的查询，游标分页（useChatList 与路由 loader 共用）。 */
export function chatListQuery(q: string, mode: ChatModeFilter, scope: ChatScope, channelId?: string) {
  const trimmed = q.trim()
  return infiniteQueryOptions({
    queryKey: [...kit.queries.chat.key(), "list", scope, mode, channelId ?? "", trimmed],
    queryFn: ({ pageParam }) =>
      tenantApi.chat.list({
        cursor: pageParam || undefined,
        scope,
        mode: mode === "all" ? undefined : mode,
        channel_id: channelId || undefined,
        q: trimmed || undefined,
        per_page: 30,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
}

/** useChat 读取单条聊天的详情及完整参与者列表；还没读到时用聊天列表中已有的那一项占位。 */
export function useChat(id: string) {
  const queryClient = useQueryClient()
  return useQuery({
    ...kit.queries.chat.get(id),
    enabled: Boolean(id),
    placeholderData: () => findInLists<ConversationChatDTO>(queryClient, [...kit.queries.chat.key(), "list"], id),
  })
}

/** chatQueries 是打开聊天 id 要用的查询：聊天和时间线（路由 loader 与预取共用）。 */
export function chatQueries(queryClient: QueryClient, id: string): Warmable[] {
  return [kit.queries.chat.get(id), timelineQuery(queryClient, id)]
}

/** prefetchChat 预取聊天 id 及其时间线：列表行悬停、J/K 切换用。 */
export function prefetchChat(queryClient: QueryClient, id: string): Promise<void> {
  return prefetch(queryClient, chatQueries(queryClient, id))
}

/** flattenChats 把游标分页的聊天结果展平并按 ID 去重。 */
export function flattenChats(pages: { items: ConversationChatDTO[] }[] | undefined): ConversationChatDTO[] {
  if (!pages) return []
  const seen = new Set<string>()
  const out: ConversationChatDTO[] = []
  for (const page of pages) {
    for (const item of page.items) {
      if (seen.has(item.id)) continue
      seen.add(item.id)
      out.push(item)
    }
  }
  return out
}

/** chatDisplayName 返回聊天的显示标题：群聊取群名，外部单聊取联系人名，内部单聊取对方同事名。 */
export function chatDisplayName(chat: ConversationChatDTO, t: T): string {
  if (chat.mode === "group") {
    return chat.name || t("chat.untitled_group")
  }
  if (chat.scope === "external") {
    return chat.contact?.name || chat.peer?.name || chat.name || t("inbox.visitor")
  }
  return chat.peer?.name || t("chat.unknown_peer")
}
