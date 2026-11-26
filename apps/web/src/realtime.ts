import { useQueryClient } from "@tanstack/react-query"
import { Centrifuge } from "centrifuge"
import { createContext, useContext, useEffect, useRef, useState } from "react"
import { kit } from "./api"
import type { PushAlert } from "./inbox/alerts"

// 实时推送：成员在每个工作区中有一个个人频道，会话有变化（新消息、指派、关闭等）或者自己在别的页面标记了已读时，
// 服务端往里面推送一个事件 { type: "conversation" | "read", id: 会话 ID, seq?: 时间线最新一项的序号 }（服务端的 push.Event）。
// 页面收到后让会话的查询失效，数据仍从接口读取。同一条连接还用来加入打开着的会话的频道（inbox/channel.ts）。

/** channelOf 返回成员 memberId 在工作区 tenantId 中的个人频道，与服务端的 push.Channel 相同。 */
const channelOf = (tenantId: string, memberId: string) => `t:${tenantId}:member:${memberId}`

/** RealtimeContext 是工作区的实时连接，由工作区的布局提供；还没有连上身份时为 null。 */
export const RealtimeContext = createContext<Centrifuge | null>(null)

/** useCentrifuge 返回工作区的实时连接，没有时为 null。 */
export const useCentrifuge = () => useContext(RealtimeContext)

/** PushEvent 是服务端推送到成员个人频道的事件（对应服务端的 push.Event）。 */
interface PushEvent {
  type?: string
  id?: string
  seq?: number
  item_seq?: number
  alert?: PushAlert
}

/** refreshFrom 记录每个会话在推送中发生变化的最早历史消息序号，useTimeline 刷新时据此覆盖历史补译的消息。 */
const refreshFrom = new Map<string, number>()

/** consumeRefreshFrom 取出并清除会话 id 需要回溯刷新的最早消息序号。 */
export function consumeRefreshFrom(id: string): number | undefined {
  const seq = refreshFrom.get(id)
  if (seq !== undefined) refreshFrom.delete(id)
  return seq
}

/**
 * useRealtime 在工作区 slug 中建立实时连接，订阅成员 memberId 的个人频道，返回这条连接交给 RealtimeContext。
 * 收到事件后让会话分组的查询（收件箱的数量、会话列表、会话和时间线）失效，200 毫秒内的多条合并成一次；
 * 若事件带有实时提醒信号（alert），立即回调 onAlert 触发桌面通知或提示音；
 * 断线重连、重新订阅之后也让查询失效，补上断线期间的变化。离开工作区、退出登录时组件卸载，连接随之断开。
 */
export function useRealtime(
  slug: string,
  tenantId: string,
  memberId: string | undefined,
  onAlert?: (ev: { type?: string; id: string; alert: PushAlert }) => void,
): Centrifuge | null {
  const queryClient = useQueryClient()
  const [client, setClient] = useState<Centrifuge | null>(null)
  const alertRef = useRef(onAlert)
  alertRef.current = onAlert
  useEffect(() => {
    if (!memberId) return
    const scheme = location.protocol === "https:" ? "wss" : "ws"
    const centrifuge = new Centrifuge(`${scheme}://${location.host}/t/${slug}/realtime`)
    let timer: ReturnType<typeof setTimeout> | undefined
    const pending = {
      all: false,
      inbox: false,
      tickets: false,
      chats: false,
      views: false,
      contacts: false,
      conversations: new Set<string>(),
    }
    const flush = () => {
      if (pending.all) {
        pending.all = false
        pending.inbox = false
        pending.tickets = false
        pending.chats = false
        pending.views = false
        pending.contacts = false
        pending.conversations.clear()
        void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
        void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
        void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
        void queryClient.invalidateQueries({ queryKey: kit.queries.view.key() })
        void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
        return
      }
      if (pending.inbox) {
        pending.inbox = false
        void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.inbox().queryKey })
        void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "inbox-list"] })
      }
      if (pending.tickets) {
        pending.tickets = false
        void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
      }
      if (pending.chats) {
        pending.chats = false
        void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
      }
      if (pending.conversations.size > 0) {
        for (const id of pending.conversations) {
          void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.get(id).queryKey })
          void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "timeline", id] })
          void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "items", id] })
          void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "rounds", id] })
        }
        pending.conversations.clear()
      }
      if (pending.views) {
        pending.views = false
        void queryClient.invalidateQueries({ queryKey: kit.queries.view.key() })
        void queryClient.invalidateQueries({ queryKey: [...kit.queries.contact.key(), "views"] })
        void queryClient.invalidateQueries({ queryKey: [...kit.queries.contact.key(), "sidebar"] })
      }
      if (pending.contacts) {
        pending.contacts = false
        void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      }
    }
    const schedule = () => {
      clearTimeout(timer)
      timer = setTimeout(flush, 200)
    }
    const refreshAll = () => {
      pending.all = true
      schedule()
    }
    // 第一次订阅成功时页面刚刚读过数据，之后每次重新订阅成功都全量补拉断线期间的变化
    let first = true
    const sub = centrifuge.newSubscription(channelOf(tenantId, memberId))
    sub.on("publication", (ctx) => {
      const ev = ctx.data as PushEvent | undefined
      if (ev?.id && typeof ev.item_seq === "number" && ev.item_seq > 0) {
        const prev = refreshFrom.get(ev.id)
        refreshFrom.set(ev.id, prev === undefined ? ev.item_seq : Math.min(prev, ev.item_seq))
      }
      if (ev?.id && ev.alert) {
        alertRef.current?.({ type: ev.type, id: ev.id, alert: ev.alert })
      }
      switch (ev?.type) {
        case "conversation":
          pending.inbox = true
          if (ev.id) pending.conversations.add(ev.id)
          break
        case "ticket":
          pending.inbox = true
          pending.tickets = true
          if (ev.id) pending.conversations.add(ev.id)
          break
        case "chat":
          pending.chats = true
          if (ev.id) pending.conversations.add(ev.id)
          break
        case "read":
          pending.inbox = true
          pending.tickets = true
          pending.chats = true
          if (ev.id) pending.conversations.add(ev.id)
          break
        case "views":
          pending.views = true
          pending.inbox = true
          break
        default:
          pending.all = true
      }
      schedule()
    })
    sub.on("subscribed", () => {
      if (!first) refreshAll()
      first = false
    })
    sub.subscribe()
    centrifuge.connect()
    setClient(centrifuge)
    return () => {
      clearTimeout(timer)
      refreshFrom.clear()
      setClient(null)
      centrifuge.disconnect()
    }
  }, [slug, tenantId, memberId, queryClient])
  return client
}
