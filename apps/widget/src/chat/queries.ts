import type {
  ConversationContactTicketDetailDTO,
  ConversationContactTicketDTO,
  ConversationContactTimelineDTO,
} from "@agents-desk/client/widget"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback, useRef, useState, useSyncExternalStore } from "react"
import { authed, widgetApi } from "../api"
import { i18n } from "../i18n"
import { useIdentity } from "../realtime"

// 聊天窗口的查询。键都以 ["widget", 联系人] 开头：实时推送让 ["widget", contact.id] 整组失效；
// 换了身份（登录、退出）时键随之变化，不会看到上一个人的数据。

/** TIMELINE_PAGE_SIZE 是首次加载和向上翻页时每页读取的消息数。 */
const TIMELINE_PAGE_SIZE = 50

/** TIMELINE_INCREMENTAL_LIMIT 是收到实时推送后单次增量拉取新消息的上限，与服务端 timeline 接口的 maximum=100 对齐。 */
const TIMELINE_INCREMENTAL_LIMIT = 100

/** MAX_INCREMENTAL_PAGES 是单次刷新时连续翻页追赶新消息的最多轮次，避免异常循环。 */
const MAX_INCREMENTAL_PAGES = 10

/** mergeItems 按消息 ID 去重合并前后两段有序消息，保持先后顺序，并把新增更正消息的更正状态同步到已缓存的原消息上。 */
function mergeItems<T extends { id: string; created_at?: string; corrects_item_id?: string; corrected_by?: string; corrected_at?: string }>(
  head: T[],
  tail: T[],
): T[] {
  if (head.length === 0) return tail
  if (tail.length === 0) return head
  const seen = new Set(head.map((it) => it.id))
  const appended = tail.filter((it) => !seen.has(it.id))
  if (appended.length === 0) return head
  const corrections = new Map<string, T>()
  for (const it of appended) {
    if (it.corrects_item_id) corrections.set(it.corrects_item_id, it)
  }
  const updatedHead =
    corrections.size === 0
      ? head
      : head.map((it) => {
          const corr = corrections.get(it.id)
          return corr ? { ...it, corrected_by: corr.id, corrected_at: corr.created_at } : it
        })
  return [...updatedHead, ...appended]
}

/** useConfig 读取聊天插件按当前访客语言解析后的公开设置：对外标题、问候语、主题色、可选语言与商务配置。 */
export function useConfig() {
  const requestedLang = useSyncExternalStore(i18n.subscribe, i18n.requestedLanguage, i18n.requestedLanguage)
  return useQuery({
    queryKey: ["widget", "config", requestedLang],
    queryFn: () => widgetApi.widget.config({ lang: requestedLang }).then((r) => r.body),
    staleTime: 5 * 60_000,
    placeholderData: (prev) => prev,
  })
}

/**
 * useTimeline 读取客户跨会话的连续时间线。
 * 首次加载最近一页；后续推送或重连刷新时只按最后一条消息 ID 增量拉取新消息，并同步更新轮次摘要、评价状态与未读数。
 * 返回的 loadOlder 用于向上翻页加载更早的历史消息并保留当前已读列表。
 */
export function useTimeline() {
  const { contact } = useIdentity()
  const queryClient = useQueryClient()
  const key = ["widget", contact?.id, "timeline"] as const
  const [loadingOlder, setLoadingOlder] = useState(false)
  const loadingOlderRef = useRef(false)

  const query = useQuery<ConversationContactTimelineDTO>({
    queryKey: key,
    queryFn: async () => {
      const prev = queryClient.getQueryData<ConversationContactTimelineDTO>(key)
      let after = prev?.items.at(-1)?.id
      if (!prev || !after) {
        return authed(() => widgetApi.timeline.get({ limit: TIMELINE_PAGE_SIZE }))
      }
      let items = prev.items
      let conversations = { ...prev.conversations }
      let latest = prev.latest_conversation
      let unread = prev.unread
      for (let page = 0; page < MAX_INCREMENTAL_PAGES; page++) {
        const next = await authed(() => widgetApi.timeline.get({ after, limit: TIMELINE_INCREMENTAL_LIMIT }))
        items = mergeItems(items, next.items)
        conversations = { ...conversations, ...next.conversations }
        latest = next.latest_conversation
        unread = next.unread
        const last = next.items.at(-1)
        if (!next.has_more || !last || last.id === after) break
        after = last.id
      }
      return {
        items,
        conversations,
        latest_conversation: latest,
        unread,
        has_more: prev.has_more,
      }
    },
    enabled: !!contact,
  })

  // loadOlder 按当前最早一条消息的 ID 向上加载更早历史并拼在列表头部，返回本次是否加载到了消息。
  const loadOlder = useCallback(async (): Promise<boolean> => {
    if (!contact || loadingOlderRef.current) return false
    const snapshot = queryClient.getQueryData<ConversationContactTimelineDTO>(key)
    const before = snapshot?.items[0]?.id
    if (!snapshot || !snapshot.has_more || !before) return false
    loadingOlderRef.current = true
    setLoadingOlder(true)
    try {
      const older = await authed(() => widgetApi.timeline.get({ before, limit: TIMELINE_PAGE_SIZE }))
      queryClient.setQueryData<ConversationContactTimelineDTO>(key, (cur) => {
        if (!cur) return older
        return {
          items: mergeItems(older.items, cur.items),
          conversations: { ...older.conversations, ...cur.conversations },
          latest_conversation: cur.latest_conversation,
          unread: cur.unread,
          has_more: older.has_more,
        }
      })
      return older.items.length > 0
    } finally {
      loadingOlderRef.current = false
      setLoadingOlder(false)
    }
  }, [contact, queryClient])

  return { ...query, loadOlder, loadingOlder }
}

/** useTickets 读取客户在工作区中关联到的客户可见工单列表。 */
export function useTickets() {
  const { contact } = useIdentity()
  return useQuery<ConversationContactTicketDTO[]>({
    queryKey: ["widget", contact?.id, "tickets"],
    queryFn: () => authed(() => widgetApi.ticket.list()),
    enabled: !!contact,
  })
}

/** useTicket 读取客户可见的单张工单详情及其公开进度列表。 */
export function useTicket(id: string) {
  const { contact } = useIdentity()
  return useQuery<ConversationContactTicketDetailDTO>({
    queryKey: ["widget", contact?.id, "ticket", id],
    queryFn: () => authed(() => widgetApi.ticket.get(id)),
    enabled: !!contact && !!id,
  })
}
