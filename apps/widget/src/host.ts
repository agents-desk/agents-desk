// 聊天窗口和外面的通信。聊天窗口有三种运行方式：
//   - 浮窗嵌入：产品网站的 loader.js 把它放在右下角 iframe 中（embed=1），通过 postMessage 和 loader.js 通信；
//   - 容器内联：H5 或单页应用在自己的页面容器中放 iframe（embed=1&inline=1），通过 postMessage 通信，默认直接展开铺满容器；
//   - 独立：App 用 WebView 整页打开，或者直接在浏览器中打开。App 在页面加载前设置 window.agentsDesk = { user_token, lang }，
//     之后调用 window.AgentsDesk(命令) 切换身份或语言；聊天窗口通过 App 提供的桥通知 App。
// 发给外面的消息只有窗口的开合、标题元数据、未读数、需要刷新身份令牌、要打开的链接，不带会话的内容。

/** PREFIX 是聊天窗口和父页面之间每条消息的类型前缀。 */
const PREFIX = "agents-desk:"

/** search 是聊天窗口页面的查询参数：channel 是聊天插件，lang 是显式语言，embed=1 表示在 iframe 中，inline=1 表示内联铺满容器，hide_close=1 表示宿主自己有关闭按钮。 */
const search = new URLSearchParams(typeof location === "undefined" ? "" : location.search)

/** channel 是这个聊天窗口的聊天插件 ID。 */
export const channel = search.get("channel") ?? ""

/** urlLang 是页面地址中显式传入的语言参数（?lang=）。 */
export const urlLang = search.get("lang") ?? ""

/** embedded 报告聊天窗口是不是被父页面放在 iframe 中（带 embed=1）。 */
export const embedded = search.get("embed") === "1" && typeof window !== "undefined" && window.parent !== window

/** inline 报告聊天窗口是不是内联铺满父页面容器（embed=1&inline=1）：不渲染右下角启动按钮，默认直接展开。 */
export const inline = embedded && search.get("inline") === "1"

// 浮窗嵌入时页面背景透明（widget.css）；容器内联与独立模式使用实底背景
if (embedded && !inline) document.documentElement.dataset.embedded = ""

/** hostHandlesOpenURL 报告嵌入模式的父页面是否在 init 握手中声明接管外链打开。 */
let hostHandlesOpenURL = false

/** parentOrigin 在 init 握手后锁定父页面源，用于校验后续 postMessage 来源并定向发送消息。 */
let parentOrigin: string | undefined

/** Command 是外面发给聊天窗口的命令。 */
export type Command =
  | { command: "open" }
  | { command: "close" }
  | { command: "login"; user_token: string }
  | { command: "logout" }
  | { command: "setLanguage"; lang: string }

/** Bridge 是 App 在 WebView 中提供的桥：iOS 的 messageHandlers.agentsDesk，Android 和 Flutter 的 AgentsDeskBridge，React Native 的 ReactNativeWebView。 */
interface Bridge {
  postMessage(message: string | object): void
}

declare global {
  interface Window {
    agentsDesk?: { channel?: string; user_token?: string; lang?: string }
    AgentsDesk?: (command: string, arg?: { user_token?: string; lang?: string }) => void
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

/** hasCloseButton 报告聊天窗口要不要显示关闭按钮：浮窗嵌入时总是显示（收起）；容器内联或独立带 App 桥时，宿主未要求隐藏（hide_close=1）则显示。 */
export function hasCloseButton(): boolean {
  if (embedded && !inline) return true
  if (inline) return search.get("hide_close") !== "1"
  return appBridge() !== undefined && search.get("hide_close") !== "1"
}

/** delegatesOpenLink 报告链接和文件下载是否交由宿主打开：独立模式有 App 桥，或嵌入模式的父页面声明了 open_url。 */
export function delegatesOpenLink(): boolean {
  return embedded ? hostHandlesOpenURL : appBridge() !== undefined
}

/** tell 把消息发给外面：嵌入时发给父页面，独立时发给 App 的桥；直接在浏览器中打开时什么也不做。 */
export function tell(
  type: "ready" | "open" | "close" | "unread" | "open_url" | "need_token" | "meta",
  data: Record<string, unknown> = {},
): void {
  if (embedded) {
    window.parent.postMessage({ type: PREFIX + type, ...data }, parentOrigin ?? "*")
    return
  }
  appBridge()?.({ type, ...data })
}

/** openLink 打开消息中的链接：宿主接管时发出 open_url 交由宿主打开，其他时候在新的标签页中打开。 */
export function openLink(url: string): void {
  if (delegatesOpenLink()) {
    tell("open_url", { url })
    return
  }
  window.open(url, "_blank", "noopener")
}

/** Init 是 loader.js 在聊天窗口准备好后发来的初始状态。 */
export interface Init {
  userToken?: string // 已登录用户的身份
  lang?: string // 宿主显式指定的语言（window.agentsDesk.lang）
  hostLang?: string // 宿主页面的 <html lang>
  mobile: boolean // 产品页面是不是手机的宽度，展开后占满屏幕
  referrer?: string // 嵌入聊天窗口的产品页面地址
}

/**
 * listen 接收外面发来的命令和布局：嵌入时只接受 window.parent 发来、带前缀的消息；独立时由 App 调用 window.AgentsDesk。
 * 返回取消监听的函数。
 */
export function listen(handlers: { command: (c: Command) => void; layout: (mobile: boolean) => void }): () => void {
  if (embedded) {
    const onMessage = (e: MessageEvent) => {
      if (e.source !== window.parent || typeof e.data?.type !== "string" || !e.data.type.startsWith(PREFIX)) return
      if (parentOrigin && e.origin !== parentOrigin) return
      const type = e.data.type.slice(PREFIX.length)
      if (type === "layout") handlers.layout(Boolean(e.data.mobile))
      if (type === "command") handlers.command(e.data as Command)
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }
  window.AgentsDesk = (command, arg) => {
    if (command === "login" && arg?.user_token) handlers.command({ command: "login", user_token: arg.user_token })
    if (command === "setLanguage" && arg?.lang) handlers.command({ command: "setLanguage", lang: arg.lang })
    if (command === "logout" || command === "open" || command === "close") handlers.command({ command })
  }
  return () => {
    window.AgentsDesk = undefined
  }
}

/**
 * waitInit 等待外面给出的初始状态：嵌入时等父页面的 init（最多一秒半，父页面未应答时按访客继续，内联模式固定铺满容器）；
 * 独立时读取 App 在页面加载前设置的 window.agentsDesk，总是占满屏幕。
 */
export function waitInit(): Promise<Init> {
  if (!embedded) {
    const referrer = typeof document !== "undefined" && document.referrer ? document.referrer : undefined
    return Promise.resolve({
      userToken: window.agentsDesk?.user_token,
      lang: window.agentsDesk?.lang,
      mobile: true,
      referrer,
    })
  }
  return new Promise((resolve) => {
    const timer = setTimeout(() => done({ mobile: inline }), 1500)
    const onMessage = (e: MessageEvent) => {
      if (e.source !== window.parent || e.data?.type !== PREFIX + "init") return
      if (e.origin && e.origin !== "null") parentOrigin = e.origin
      hostHandlesOpenURL = Boolean(e.data.open_url)
      done({
        userToken: e.data.user_token || undefined,
        lang: typeof e.data.lang === "string" && e.data.lang ? e.data.lang : undefined,
        hostLang: typeof e.data.host_lang === "string" && e.data.host_lang ? e.data.host_lang : undefined,
        mobile: inline || Boolean(e.data.mobile),
        referrer: typeof e.data.referrer === "string" && e.data.referrer ? e.data.referrer : undefined,
      })
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
