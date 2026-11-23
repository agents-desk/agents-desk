// 客户的身份：聊天窗口建立的会话（POST /session）得到的联系人令牌，保存在聊天窗口自己的 localStorage 中，产品网站和 App
// 的代码都读不到。按聊天插件分开保存：
//   - visitor：访客编号，识别同一位线索，退出登录后回到这位线索；
//   - session：令牌、联系人，以及它属于哪个用户（用户身份 JWT 中的 sub，线索为空）。
// 浏览器禁止存储时（隐私模式、Safari 清除了第三方存储）只保存在内存中，关闭页面后是新的线索。
import { type Client, type ContactSummary, isApiError } from "@agents-desk/client/widget"
import { channel, tell } from "./host"

/** DEFAULT_TOKEN_TIMEOUT_MS 是向宿主请求新 user_token 的默认超时时间（5 秒）。 */
const DEFAULT_TOKEN_TIMEOUT_MS = 5000

/** Session 是保存的会话。 */
interface Session {
  token: string
  contact: ContactSummary
  user: string // 用户身份中的用户编号；线索为空
}

/** IdentityOptions 是客户身份存储的可选配置。 */
export interface IdentityOptions {
  requestToken?: () => void // 需要宿主刷新已过期的 user_token 时触发
  tokenTimeoutMs?: number // 等待宿主回传新 user_token 的超时毫秒数
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

/** decodePayload 解码 JWT 的载荷段，仅用于读取 sub 与 exp；签名始终由服务端校验。 */
function decodePayload(userToken: string): Record<string, unknown> | null {
  try {
    const part = userToken.split(".")[1]
    if (!part) return null
    const base64 = part.replace(/-/g, "+").replace(/_/g, "/")
    const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4)
    return JSON.parse(atob(padded)) as Record<string, unknown>
  } catch {
    return null
  }
}

/** subjectOf 返回用户身份 JWT 中的用户编号（sub），只用来判断是不是同一个用户；签名由服务端校验。 */
function subjectOf(userToken: string): string {
  const payload = decodePayload(userToken)
  return payload && payload.sub !== undefined && payload.sub !== null ? String(payload.sub) : ""
}

/** isTokenExpired 报告用户身份 JWT 的 exp 字段是否已早于当前时间。 */
function isTokenExpired(userToken: string): boolean {
  const payload = decodePayload(userToken)
  return typeof payload?.exp === "number" && payload.exp <= Math.floor(Date.now() / 1000)
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

/** hasStoredIdentity 报告浏览器中是否已保存访客编号或会话：有表示回头访客或已登录会话，收起态也应恢复以展示未读数。 */
function hasStoredIdentity(): boolean {
  return load("visitor") !== null || readSession() !== null
}

/**
 * createIdentity 返回客户身份的存储：
 *   - 仅当满足“窗口已打开 || 有已登录 user_token || 本地已有访客/会话记录”时才建立或恢复后端会话，
 *     避免匿名访客仅浏览宿主页面、尚未点开聊天窗口就提前创建空线索；
 *   - start 在宿主握手完成后调用；ensure 在客户第一次展开聊天窗口时激活身份；
 *   - login 在同用户续签时静默刷新 user_token 并返回 false，仅在用户变化时切换会话并返回 true；
 *   - renew 在联系人令牌失效时重建会话，缓存的 user_token 过期或失效时按需通过 requestToken 向宿主请求新令牌；
 *   - subscribe 在会话变化时通知（实时连接、查询随之更新）。
 */
export function createIdentity(options: IdentityOptions = {}) {
  const requestToken = options.requestToken
  const tokenTimeoutMs = options.tokenTimeoutMs ?? DEFAULT_TOKEN_TIMEOUT_MS
  let api: Client | undefined
  let current: Session | null = null
  let userToken: string | undefined // 当前的用户身份；线索为空
  let referrer: string | undefined // 嵌入聊天窗口的产品页面地址
  let pending: Promise<void> | undefined
  let renewing: Promise<void> | undefined // 正在重新建立的会话
  let tokenWaiter: { sub: string; resolve: (token: string | undefined) => void } | undefined
  let gen = 0 // 身份切换/登出的代际计数，避免异步续签在登出或换号后覆盖新会话
  let started!: () => void
  const startedOnce = new Promise<void>((resolve) => (started = resolve))
  const listeners = new Set<() => void>()
  const changed = () => {
    for (const l of [...listeners]) l()
  }

  // settleTokenWaiter 结束当前挂起的续签等待者，避免重复通知或泄漏定时器。
  const settleTokenWaiter = (token: string | undefined) => {
    const w = tokenWaiter
    if (!w) return
    tokenWaiter = undefined
    w.resolve(token)
  }

  // awaitFreshToken 向宿主请求一枚新的 user_token，在超时内等待宿主通过 login 回传。
  const awaitFreshToken = (sub: string): Promise<string | undefined> => {
    if (!requestToken) return Promise.resolve(undefined)
    return new Promise((resolve) => {
      const timer = setTimeout(() => settleTokenWaiter(undefined), tokenTimeoutMs)
      tokenWaiter = {
        sub,
        resolve: (next) => {
          clearTimeout(timer)
          resolve(next)
        },
      }
      requestToken()
    })
  }

  // postSession 调用 POST /session 建立联系人会话并持久化到本地存储；请求期间已登出或换号时丢弃旧结果。
  const postSession = async (token: string | undefined, myGen: number) => {
    if (!api) throw new Error("identity: api not connected")
    const timeZone = (() => {
      try {
        return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined
      } catch {
        return undefined
      }
    })()
    const res = await api.widget.session({
      visitor_id: load("visitor") ?? undefined,
      user_token: token,
      time_zone: timeZone,
      referrer,
    })
    if (myGen !== gen) return
    if (res.visitor_id) save("visitor", res.visitor_id)
    else if (token) save("visitor", null)
    current = { token: res.token, contact: res.contact, user: token ? subjectOf(token) : "" }
    save("session", JSON.stringify(current))
    changed()
  }

  // establish 建立会话：如果当前 userToken 已过期（或在重建会话时被服务端拒绝），按需向宿主请求一次新令牌后再建立会话。
  const establish = async (allowRefreshOn401: boolean) => {
    const myGen = gen
    let token = userToken
    let triedRefresh = false
    if (token && isTokenExpired(token)) {
      triedRefresh = true
      const fresh = await awaitFreshToken(subjectOf(token))
      if (myGen !== gen) return
      if (fresh) {
        token = fresh
        userToken = fresh
      }
    }
    try {
      await postSession(token, myGen)
    } catch (err) {
      if (myGen !== gen) return
      if (triedRefresh || !allowRefreshOn401 || !token || !isApiError(err) || err.status !== 401) throw err
      const fresh = await awaitFreshToken(subjectOf(token))
      if (myGen !== gen) return
      if (!fresh) throw err
      userToken = fresh
      await postSession(fresh, myGen)
    }
  }

  // switchTo 切换到用户身份 token（undefined 是线索）：
  //   - 内存中已经是同一用户时仅更新 userToken（并在续签等待中唤醒等待者），返回 false，不打断当前聊天页；
  //   - 否则命中本地保存会话直接恢复，或重新建立会话，返回 true。
  const switchTo = async (token: string | undefined): Promise<boolean> => {
    const wanted = token ? subjectOf(token) : ""
    if (token && tokenWaiter) {
      if (wanted === tokenWaiter.sub) {
        userToken = token
        settleTokenWaiter(token)
        if (pending) await pending
        return false
      }
      settleTokenWaiter(undefined)
    }
    userToken = token
    if (current && current.user === wanted) {
      return false
    }
    gen++
    const saved = readSession()
    if (saved && saved.user === wanted) {
      current = saved
      changed()
      pending = undefined
      return true
    }
    const p = establish(false).finally(() => {
      if (pending === p) pending = undefined
    })
    pending = p
    await p
    return true
  }

  return {
    /** connect 交给它调用接口的客户端。 */
    connect(client: Client) {
      api = client
    },
    /** start 在宿主握手完成后保存初始身份和来源页面；只有窗口已打开、已登录或本地已有身份时才立即建会话。 */
    async start(token: string | undefined, ref: string | undefined, active: boolean) {
      referrer = ref
      userToken = token
      try {
        if (active || token || hasStoredIdentity()) {
          await switchTo(token)
        }
      } finally {
        started()
      }
    },
    /** ensure 在客户展开聊天窗口时确保已建立会话；已有会话或正在建立时直接复用，避免重复建线索。 */
    async ensure() {
      if (pending) {
        await pending
        return
      }
      const wanted = userToken ? subjectOf(userToken) : ""
      if (current && current.user === wanted) return
      await switchTo(userToken)
    },
    /** login 切换到产品网站登录的用户，或静默刷新同一用户的 user_token；返回值报告身份是否发生变化。 */
    login: (token: string): Promise<boolean> => switchTo(token),
    /** logout 退出登录：丢掉用户会话；窗口打开或本地留有访客编号时回到访客线索，否则保持静默直到下次打开窗口。 */
    async logout(active: boolean) {
      gen++
      settleTokenWaiter(undefined)
      save("session", null)
      userToken = undefined
      if (active || load("visitor") !== null) {
        current = null
        await switchTo(undefined)
        return
      }
      current = null
      pending = undefined
      changed()
    },
    /** renew 令牌失效时重新建立会话；几个请求同时失效时只建立一次。 */
    renew() {
      if (!renewing) {
        save("session", null)
        const p = establish(true).finally(() => {
          renewing = undefined
          if (pending === p) pending = undefined
        })
        renewing = p
        pending = p
      }
      return renewing
    },
    /** ready 等到初始化和进行中的会话建立完成。 */
    async ready() {
      await startedOnce
      if (pending) await pending
    },
    /** updateContact 在客户留下邮箱等资料后更新当前会话中的联系人，并通知订阅者。 */
    updateContact(contact: ContactSummary) {
      if (!current) return
      current = { ...current, contact }
      save("session", JSON.stringify(current))
      changed()
    },
    token: () => current?.token,
    contact: () => current?.contact,
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

export const identity = createIdentity({ requestToken: () => tell("need_token") })
