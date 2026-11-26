import { useQueryClient } from "@tanstack/react-query"
import { Centrifuge } from "centrifuge"
import { useEffect, useSyncExternalStore } from "react"
import { channel } from "./host"
import { identity } from "./identity"

// 实时推送：客户在聊天插件中有一个频道 w:{channel}:contact:{contact}。成员回复、会话的状态变化、客户在别的页面发了消息时，
// 服务端往里面推送 { type: "conversation", id: 会话 ID, seq: 时间线最新一项的序号 }。收到后让会话的查询失效，数据仍从接口读取。

/** useIdentity 返回当前的联系人令牌和联系人，会话变化时重新渲染。 */
export function useIdentity() {
  const token = useSyncExternalStore(identity.subscribe, identity.token, identity.token)
  const contact = useSyncExternalStore(identity.subscribe, identity.contact, identity.contact)
  return { token, contact }
}

/**
 * useRealtime 连接 /w/{channel}/realtime，订阅客户自己的频道。收到推送后让当前联系人的连续时间线和工单查询失效，
 * 200 毫秒内的多条合并成一次；断线重连、重新订阅之后，以及页面回到前台时也让它们失效，补上这段时间的变化。
 * 身份变化（登录、退出、令牌失效后重新建立）时换一条连接。
 */
export function useRealtime(): void {
  const queryClient = useQueryClient()
  const { token, contact } = useIdentity()
  useEffect(() => {
    if (!token || !contact) return
    const scheme = location.protocol === "https:" ? "wss" : "ws"
    const centrifuge = new Centrifuge(`${scheme}://${location.host}/w/${encodeURIComponent(channel)}/realtime`, {
      token,
      // 长连接令牌过期时通过 identity.renew 续签联系人令牌。
      getToken: async () => {
        await identity.renew()
        return identity.token() ?? ""
      },
    })
    let timer: ReturnType<typeof setTimeout> | undefined
    const refresh = () => {
      clearTimeout(timer)
      // 只失效当前联系人的会话与时间线查询，不重复刷新 ["widget", "config"]。
      timer = setTimeout(() => void queryClient.invalidateQueries({ queryKey: ["widget", contact.id] }), 200)
    }
    // 第一次订阅成功时页面刚刚读过数据，之后每次重新订阅成功都重新读取
    let first = true
    const sub = centrifuge.newSubscription(`w:${channel}:contact:${contact.id}`)
    sub.on("publication", refresh)
    sub.on("subscribed", () => {
      if (!first) refresh()
      first = false
    })
    sub.subscribe()
    centrifuge.connect()
    // 手机浏览器和 App 切到后台时连接会断开，回到前台时重新读取
    const onVisible = () => {
      if (document.visibilityState === "visible") refresh()
    }
    document.addEventListener("visibilitychange", onVisible)
    return () => {
      clearTimeout(timer)
      document.removeEventListener("visibilitychange", onVisible)
      centrifuge.disconnect()
    }
  }, [token, contact, queryClient])
}
