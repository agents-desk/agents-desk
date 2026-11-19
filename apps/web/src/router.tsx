import { host } from "@go-tenon/host"
import { cspNonce } from "@go-tenon/start"
import { createApp, NotFoundPage, RouteError } from "@go-tenon/ui"
import { api, session, tenantApi } from "./api"
import { routeTree } from "./routeTree.gen"

// getRouter 创建应用：路由、TanStack Query、会话、权限和工作区，见框架文档的“应用骨架”和“租户”。
export function getRouter() {
  const app = createApp({
    id: "web",
    routeTree,
    session,
    // 权限按工作区区分：进入工作区之后读取成员在其中的权限，还没有进入时没有权限。
    permissions: async () => (session.identity()?.tenant ? (await tenantApi.tenant.current()).permissions : []),
    // 工作区的标识是 slug（地址 /t/{slug}/）。已停用的工作区不能进入，不出现在切换的列表中。
    tenancy: {
      tenants: async () =>
        (await api.tenant.mine()).filter((t) => t.status === "active").map((t) => ({ id: t.slug, name: t.name })),
      home: (slug) => `/t/${slug}/`,
    },
    router: { ssr: { nonce: cspNonce() }, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  })
  // 在桌面端中，点击通知、打开深度链接时跳到对应的页面（路径与网页上的相同）。
  host()?.on("navigate", (path) => app.router.history.push(path))
  return app.router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
