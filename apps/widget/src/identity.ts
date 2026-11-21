// 客户的身份：聊天窗口建立的会话（POST /session）得到的联系人令牌，保存在聊天窗口自己的 localStorage 中，产品网站和 App
// 的代码都读不到。按聊天插件分开保存：
//   - visitor：访客编号，识别同一位线索，退出登录后回到这位线索；
//   - session：令牌、联系人，以及它属于哪个用户（用户身份 JWT 中的 sub，线索为空）。
// 浏览器禁止存储时（隐私模式、Safari 清除了第三方存储）只保存在内存中，关闭页面后是新的线索。
import type { Client, ContactSummary } from "@agents-desk/client/widget"
import { channel } from "./host"

/** Session 是保存的会话。 */
interface Session {
  token: string
  contact: ContactSummary
  user: string // 用户身份中的用户编号；线索为空
}

/** memory 是 localStorage 不可用时的存储。 */
const memory = new Map<string, string>()

/** key 返回这个聊天插件的存储键。 */
const key = (name: string) => `agents-desk.widget.${channel}.${name}`

/** load 读取存储的值，没有或者存储不可用时返回 null。 */
function load(name: string): string | null {
  try {
    return localStorage.getItem(key(name))
  } catch {
    return memory.get(key(name)) ?? null
  }
}

/** save 保存值；value 为 null 时删除。 */
function save(name: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(key(name))
    else localStorage.setItem(key(name), value)
  } catch {
    if (value === null) memory.delete(key(name))
    else memory.set(key(name), value)
  }
}

/** subjectOf 返回用户身份 JWT 中的用户编号（sub），只用来判断是不是同一个用户；签名由服务端校验。 */
function subjectOf(userToken: string): string {
  try {
    const payload = userToken.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")
    return String(JSON.parse(atob(payload)).sub ?? "")
  } catch {
    return ""
  }
}

/** readSession 读取保存的会话，格式不对时当作没有。 */
function readSession(): Session | null {
  try {
    const s = JSON.parse(load("session") ?? "null") as Session | null
    return s && typeof s.token === "string" ? s : null
  } catch {
    return null
  }
}

/**
 * createIdentity 返回客户身份的存储：start 按初始的用户身份建立或复用会话，login、logout 在产品网站登录、退出后切换，
 * renew 在令牌失效时重新建立。subscribe 在会话变化时通知（实时连接、查询随之更新）。
 */
function createIdentity() {
  let api: Client | undefined
  let current: Session | null = null
  let userToken: string | undefined // 当前的用户身份；线索为空
  let pending: Promise<void> | undefined
  let renewing: Promise<void> | undefined // 正在重新建立的会话
  let started!: () => void
  const startedOnce = new Promise<void>((resolve) => (started = resolve))
  const listeners = new Set<() => void>()
  const changed = () => {
    for (const l of [...listeners]) l()
  }

  // establish 建立会话：带上访客编号和用户身份，保存返回的令牌和访客编号
  const establish = async () => {
    if (!api) throw new Error("identity: api not connected")
    const res = await api.widget.session({ visitor_id: load("visitor") ?? undefined, user_token: userToken })
    if (res.visitor_id) save("visitor", res.visitor_id)
    current = { token: res.token, contact: res.contact, user: userToken ? subjectOf(userToken) : "" }
    save("session", JSON.stringify(current))
    changed()
  }
  // switchTo 切换到用户身份 token（undefined 是线索）：保存的会话属于同一个人时直接用，否则重新建立
  const switchTo = (token: string | undefined) => {
    userToken = token
    const wanted = token ? subjectOf(token) : ""
    const saved = readSession()
    if (saved && saved.user === wanted) {
      current = saved
      changed()
      pending = Promise.resolve()
    } else {
      pending = establish()
    }
    return pending
  }

  return {
    /** connect 交给它调用接口的客户端。 */
    connect(client: Client) {
      api = client
    },
    /** start 按初始的用户身份建立会话，聊天窗口拿到初始状态后调用一次。 */
    async start(token: string | undefined) {
      await switchTo(token)
      started()
    },
    /** login 切换到产品网站新登录的用户。 */
    login: (token: string) => switchTo(token),
    /** logout 回到访客的线索：丢掉用户的会话，带着访客编号重新建立。 */
    logout() {
      save("session", null)
      return switchTo(undefined)
    },
    /** renew 令牌失效时重新建立会话；几个请求同时失效时只建立一次。 */
    renew() {
      if (!renewing) {
        save("session", null)
        renewing = establish().finally(() => {
          renewing = undefined
        })
        pending = renewing
      }
      return renewing
    },
    /** ready 等到会话建立好。 */
    async ready() {
      await startedOnce
      await pending
    },
    token: () => current?.token,
    contact: () => current?.contact,
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

export const identity = createIdentity()
