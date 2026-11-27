import { host } from "@go-tenon/host"
import { cspNonce } from "@go-tenon/start"
import { createApp, NotFoundPage, RouteError } from "@go-tenon/ui"
import { api, session, tenantApi } from "./api"
import { i18n } from "./i18n"
import { bindObserve, reportRouteError } from "./observe"
import { routeTree } from "./routeTree.gen"

// getRouter 创建应用：路由、TanStack Query、会话、权限、工作区和界面文字，见框架文档的“应用骨架”“租户”和“界面文字”。
export function getRouter() {
  const app = createApp({
    id: "web",
    routeTree,
    session,
    i18n,
    // 登录后无回跳地址时进入工作台智能入口 /app/（自动跳上次工作区或工作区选择页）。
    homePath: "/",
    // 权限按工作区区分：进入工作区之后读取成员在其中的权限，还没有进入时没有权限。
    permissions: async () => (session.identity()?.tenant ? (await tenantApi.tenant.current()).permissions : []),
    // 工作区的标识是 slug（地址 /app/{slug}/）。已停用的工作区不能进入，不出现在切换的列表中。
    tenancy: {
      tenants: async () =>
        (await api.tenant.mine()).filter((t) => t.status === "active").map((t) => ({ id: t.slug, name: t.name })),
      home: (slug) => `/${slug}/`,
    },
    onError: reportRouteError,
    router: { ssr: { nonce: cspNonce() }, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  })
  bindObserve(app)
  // 在桌面端中，点击通知、打开深度链接时跳到对应的页面（路径与网页上的相同）。
  host()?.on("navigate", (path) => app.router.history.push(path))
  return app.router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
