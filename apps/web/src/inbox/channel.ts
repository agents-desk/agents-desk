import type { Subscription } from "centrifuge"
import { useCallback, useEffect, useRef, useState } from "react"
import { useCentrifuge } from "../realtime"

// 会话的频道 t:{tenant}:conversation:{id}：打开着会话的成员加入它，彼此看到谁正在看（presence）；成员输入时由页面
// 发布 {type: "typing"}，客户输入时由服务端发布 {type: "typing", actor: {type: "contact"}}。“正在输入”几秒内没有新的就消失。

/** Viewer 是正在看这个会话的一位成员。 */
export interface Viewer {
  id: string // 成员 ID
  name: string
}

/** TypingNow 是正在输入的人：成员（带 ID、名字与模式）或者客户。 */
export interface TypingNow {
  key: string
  memberId?: string
  name?: string
  contact: boolean
  mode?: "reply" | "note"
}

/** TYPING_MS 是“正在输入”显示的时长：页面每隔 3 秒发一次，超过它没有新的就消失。 */
const TYPING_MS = 5000

/** memberIdOf 从实时连接的主体（member:<id>）中取出成员 ID。 */
const memberIdOf = (user: string | undefined) => (user?.startsWith("member:") ? user.slice("member:".length) : undefined)

/**
 * useConversationChannel 在会话 id 打开期间加入它的频道，返回其他正在看的成员、正在输入的人，以及告诉别人自己正在输入的函数。
 * selfId 是当前成员：自己不出现在两个列表中。
 */
export function useConversationChannel(tenantId: string, id: string, selfId: string | undefined) {
  const centrifuge = useCentrifuge()
  const [viewers, setViewers] = useState<Viewer[]>([])
  const [typing, setTyping] = useState<TypingNow[]>([])
  const sub = useRef<Subscription | null>(null)
  const lastSent = useRef(0)
  const lastMode = useRef<"reply" | "note">("reply")

  useEffect(() => {
    if (!centrifuge || !selfId) return
    const channel = `t:${tenantId}:conversation:${id}`
    const s = centrifuge.getSubscription(channel) ?? centrifuge.newSubscription(channel)
    sub.current = s
    const timers = new Map<string, ReturnType<typeof setTimeout>>()

    // 在线成员：订阅成功和有人加入、离开时重新读取整个列表，一个成员的多个页面算一个人
    const reload = async () => {
      const p = await s.presence().catch(() => null)
      if (!p) return
      const seen = new Map<string, Viewer>()
      for (const c of Object.values(p.clients)) {
        const memberId = memberIdOf(c.user)
        const info = c.chanInfo as { name?: string } | undefined
        if (memberId && memberId !== selfId) seen.set(memberId, { id: memberId, name: info?.name ?? "" })
      }
      setViewers([...seen.values()])
    }
    const onPublication = (ctx: { data: unknown; info?: { user?: string; chanInfo?: unknown } }) => {
      const data = ctx.data as { type?: string; mode?: "reply" | "note"; actor?: { type?: string } }
      if (data?.type !== "typing") return
      const memberId = memberIdOf(ctx.info?.user)
      if (memberId === selfId) return
      const contact = data.actor?.type === "contact"
      const key = contact ? "contact" : (memberId ?? "")
      const name = (ctx.info?.chanInfo as { name?: string } | undefined)?.name
      setTyping((all) => [...all.filter((x) => x.key !== key), { key, memberId, name, contact, mode: data.mode }])
      clearTimeout(timers.get(key))
      timers.set(key, setTimeout(() => setTyping((all) => all.filter((x) => x.key !== key)), TYPING_MS))
    }
    s.on("subscribed", reload)
    s.on("join", reload)
    s.on("leave", reload)
    s.on("publication", onPublication)
    s.subscribe()
    // 实例崩溃时不发离开事件，定期读取一次
    const poll = setInterval(reload, 30_000)
    return () => {
      clearInterval(poll)
      for (const t of timers.values()) clearTimeout(t)
      s.removeAllListeners()
      s.unsubscribe()
      centrifuge.removeSubscription(s)
      sub.current = null
      setViewers([])
      setTyping([])
    }
  }, [centrifuge, tenantId, id, selfId])

  // notifyTyping 告诉别人自己正在输入，每 3 秒最多一次；切换回复与备注模式时立即通知一次
  const notifyTyping = useCallback((mode: "reply" | "note" = "reply") => {
    if (!sub.current) return
    const now = Date.now()
    if (now - lastSent.current < 3000 && lastMode.current === mode) return
    lastSent.current = now
    lastMode.current = mode
    void sub.current.publish({ type: "typing", mode }).catch(() => {
      // “正在输入”是尽力而为的提示：发不出去时什么也不做
    })
  }, [])
  // clearTyping 在 key（contact 或成员 ID）的消息到达时清掉他的“正在输入”，不用等到超时
  const clearTyping = useCallback((key: string) => setTyping((all) => all.filter((x) => x.key !== key)), [])
  return { viewers, typing, notifyTyping, clearTyping }
}
