import type { ChatMode, ChatScope, ConversationChatDTO } from "@agents-desk/client/tenant"
import { useInfiniteQuery, useQuery } from "@tanstack/react-query"
import type { useT } from "@go-tenon/ui"
import { kit, tenantApi } from "../api"

type T = ReturnType<typeof useT>

/** ChatModeFilter 是聊天列表的模式筛选：全部、仅单聊或仅群聊。 */
export type ChatModeFilter = "all" | ChatMode

/** useChatList 按范围（内部聊天/商务沟通）、模式、渠道与搜索词拉取当前成员参与的聊天列表（游标无限滚动）。 */
export function useChatList(q: string, mode: ChatModeFilter, scope: ChatScope, channelId?: string) {
  const trimmed = q.trim()
  return useInfiniteQuery({
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

/** useChat 读取单条聊天的详情及完整参与者列表。 */
export function useChat(id: string) {
  return useQuery({
    ...kit.queries.chat.get(id),
    enabled: Boolean(id),
  })
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
