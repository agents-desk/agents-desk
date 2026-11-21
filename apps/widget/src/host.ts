// 聊天窗口和外面的通信。聊天窗口有两种运行方式：
//   - 嵌入：产品网站的 loader.js 把它放在 iframe 中（地址带 embed=1），通过 postMessage 和 loader.js 通信；
//   - 独立：App 用 WebView 整页打开，或者直接在浏览器中打开。App 在页面加载前设置 window.agentsDesk = { user_token }，
//     之后调用 window.AgentsDesk(命令) 切换身份；聊天窗口通过 App 提供的桥通知 App。
// 发给外面的消息只有窗口的开合、未读数、要打开的链接，不带会话的内容。

/** PREFIX 是聊天窗口和 loader.js 之间每条消息的类型前缀。 */
const PREFIX = "agents-desk:"

/** search 是聊天窗口页面的查询参数：channel 是聊天插件，embed=1 表示在 iframe 中，hide_close=1 表示 App 自己有关闭按钮。 */
const search = new URLSearchParams(typeof location === "undefined" ? "" : location.search)

/** channel 是这个聊天窗口的聊天插件 ID。 */
export const channel = search.get("channel") ?? ""

/** embedded 报告聊天窗口是不是被 loader.js 放在 iframe 中。 */
export const embedded = search.get("embed") === "1" && typeof window !== "undefined" && window.parent !== window

// 嵌入时页面背景透明（widget.css）：页面外壳是构建时生成的，属性在浏览器中加上
if (embedded) document.documentElement.dataset.embedded = ""

/** Command 是外面发给聊天窗口的命令。 */
export type Command =
  | { command: "open" }
  | { command: "close" }
  | { command: "login"; user_token: string }
  | { command: "logout" }

/** Bridge 是 App 在 WebView 中提供的桥：iOS 的 messageHandlers.agentsDesk，Android 和 Flutter 的 AgentsDeskBridge，React Native 的 ReactNativeWebView。 */
interface Bridge {
  postMessage(message: string | object): void
}

declare global {
  interface Window {
    agentsDesk?: { channel?: string; user_token?: string }
    AgentsDesk?: (command: string, arg?: { user_token?: string }) => void
    AgentsDeskBridge?: Bridge
    ReactNativeWebView?: Bridge
    webkit?: { messageHandlers?: { agentsDesk?: Bridge } }
  }
}

/** appBridge 返回 App 提供的桥；直接在浏览器中打开时没有。 */
function appBridge(): ((message: object) => void) | undefined {
  if (typeof window === "undefined") return undefined
  const ios = window.webkit?.messageHandlers?.agentsDesk
  if (ios) return (m) => ios.postMessage(m)
  const android = window.AgentsDeskBridge
  if (android) return (m) => android.postMessage(JSON.stringify(m))
  const rn = window.ReactNativeWebView
  if (rn) return (m) => rn.postMessage(JSON.stringify(m))
  return undefined
}

/** hasCloseButton 报告聊天窗口要不要显示关闭按钮：嵌入时总是显示（收起）；独立时有 App 的桥、并且 App 没有要求隐藏时显示。 */
export function hasCloseButton(): boolean {
  if (embedded) return true
  return appBridge() !== undefined && search.get("hide_close") !== "1"
}

/** tell 把消息发给外面：嵌入时发给 loader.js，独立时发给 App 的桥；直接在浏览器中打开时什么也不做。 */
export function tell(type: "ready" | "open" | "close" | "unread" | "open_url", data: Record<string, unknown> = {}): void {
  if (embedded) {
    window.parent.postMessage({ type: PREFIX + type, ...data }, "*")
    return
  }
  appBridge()?.({ type, ...data })
}

/** openLink 打开消息中的链接：App 中交给 App 用系统浏览器打开，其他时候在新的标签页中打开。 */
export function openLink(url: string): void {
  const bridge = !embedded && appBridge()
  if (bridge) {
    bridge({ type: "open_url", url })
    return
  }
  window.open(url, "_blank", "noopener")
}

/** Init 是 loader.js 在聊天窗口准备好后发来的初始状态。 */
export interface Init {
  userToken?: string // 已登录用户的身份
  mobile: boolean // 产品页面是不是手机的宽度，展开后占满屏幕
}

/**
 * listen 接收外面发来的命令和布局：嵌入时只接受 window.parent 发来、带前缀的消息；独立时由 App 调用 window.AgentsDesk。
 * 返回取消监听的函数。
 */
export function listen(handlers: { command: (c: Command) => void; layout: (mobile: boolean) => void }): () => void {
  if (embedded) {
    const onMessage = (e: MessageEvent) => {
      if (e.source !== window.parent || typeof e.data?.type !== "string" || !e.data.type.startsWith(PREFIX)) return
      const type = e.data.type.slice(PREFIX.length)
      if (type === "layout") handlers.layout(Boolean(e.data.mobile))
      if (type === "command") handlers.command(e.data as Command)
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }
  window.AgentsDesk = (command, arg) => {
    if (command === "login" && arg?.user_token) handlers.command({ command: "login", user_token: arg.user_token })
    if (command === "logout" || command === "open" || command === "close") handlers.command({ command })
  }
  return () => {
    window.AgentsDesk = undefined
  }
}

/**
 * waitInit 等待外面给出的初始状态：嵌入时等 loader.js 的 init（最多一秒半，loader.js 不在时按访客、电脑宽度继续）；
 * 独立时读取 App 在页面加载前设置的 window.agentsDesk，总是占满屏幕。
 */
export function waitInit(): Promise<Init> {
  if (!embedded) return Promise.resolve({ userToken: window.agentsDesk?.user_token, mobile: true })
  return new Promise((resolve) => {
    const timer = setTimeout(() => done({ mobile: false }), 1500)
    const onMessage = (e: MessageEvent) => {
      if (e.source !== window.parent || e.data?.type !== PREFIX + "init") return
      done({ userToken: e.data.user_token || undefined, mobile: Boolean(e.data.mobile) })
    }
    const done = (init: Init) => {
      clearTimeout(timer)
      window.removeEventListener("message", onMessage)
      resolve(init)
    }
    window.addEventListener("message", onMessage)
    tell("ready")
  })
}
