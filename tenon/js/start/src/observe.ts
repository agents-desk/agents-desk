// 浏览器端可观测性桥接：在 SSR 与浏览器端之间统一托管 sink 注册、路由错误上报、页面名与登录身份同步。

/** 单个字段的校验错误信息，与 @go-tenon/client 的 FieldError 结构一致。 */
export interface ObserveFieldError {
  location?: string
  message: string
  code?: string
  value?: unknown
}

/** 页面错误中的接口元信息，与 @go-tenon/client 的 ErrorInfo 和 @go-tenon/ui 的 ErrorReport 兼容。 */
export interface ObserveErrorInfo {
  kind?: string
  status?: number
  code?: string
  message?: string
  fields?: readonly ObserveFieldError[]
  params?: Record<string, unknown>
  retryAfter?: number
  requestId?: string
}

/** 路由错误上报载荷，与 @go-tenon/ui 的 ErrorReport 兼容。 */
export interface ObserveReport {
  routeId: string
  pathname: string
  error: ObserveErrorInfo
}

/** 浏览器端观测桥接接口，由 client.tsx 在读取到 Go 下发的配置后注入具体 SDK（例如 OpenObserve）。 */
export interface ObserveSink {
  reportError: (error: unknown, report: ObserveReport) => void
  setViewName: (name: string) => void
  setUser: (id: string, tenant: string) => void
  clearUser: () => void
}

/** 可绑定路由与身份监听的最小应用结构；匿名应用可省略 session。 */
export interface ObservableApp {
  router: {
    state: { matches: ReadonlyArray<{ routeId: string }> }
    subscribe: (event: "onResolved", listener: () => void) => unknown
  }
  session?: {
    identity: () => { account: string; tenant?: string } | null
    subscribe: (listener: () => void) => unknown
  }
}

/** 浏览器观测桥接实例接口。 */
export interface ObserveBridge {
  enable: (sink: ObserveSink) => void
  reportError: (error: unknown, report: ObserveReport) => void
  bind: (app: ObservableApp) => void
}

/** 创建独立的观测桥接实例，便于按前端或测试隔离状态。 */
export function createObserveBridge(): ObserveBridge {
  let sink: ObserveSink | null = null
  let pendingApp: ObservableApp | null = null

  const attach = (app: ObservableApp, current: ObserveSink): void => {
    const syncRoute = () => {
      const match = app.router.state.matches.at(-1)
      if (match) current.setViewName(match.routeId)
    }
    app.router.subscribe("onResolved", syncRoute)
    syncRoute()

    if (!app.session) return
    const session = app.session
    const syncUser = () => {
      const id = session.identity()
      if (id) {
        current.setUser(id.account, id.tenant ?? "")
      } else {
        current.clearUser()
      }
    }
    session.subscribe(syncUser)
    syncUser()
  }

  return {
    enable(next) {
      sink = next
      if (pendingApp) {
        attach(pendingApp, next)
        pendingApp = null
      }
    },
    reportError(error, report) {
      sink?.reportError(error, report)
    },
    bind(app) {
      if (typeof window === "undefined") return
      if (!sink) {
        pendingApp = app
        return
      }
      attach(app, sink)
    },
  }
}

const defaultBridge = createObserveBridge()

/** 启用浏览器端观测，若应用已先行创建则立即补绑监听。 */
export const enableObserve: (sink: ObserveSink) => void = defaultBridge.enable

/** 上报路由错误组件捕获的页面错误；未启用观测时为空操作。 */
export const reportRouteError: (error: unknown, report: ObserveReport) => void = defaultBridge.reportError

/** 绑定路由页面名与可选登录身份同步；仅在浏览器端生效。 */
export const bindObserve: (app: ObservableApp) => void = defaultBridge.bind
