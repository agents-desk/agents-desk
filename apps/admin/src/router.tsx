import { host } from "@go-tenon/host"
import { cspNonce } from "@go-tenon/start"
import { createApp, NotFoundPage, RouteError } from "@go-tenon/ui"
import { i18n } from "./api"
import { me, session } from "./auth"
import { routeTree } from "./routeTree.gen"

// getRouter 创建应用：路由、TanStack Query、会话、权限和界面文字，见框架文档的“应用骨架”和“界面文字”。
// 服务端渲染时每个请求调用一次，所以每次都创建新的应用。
export function getRouter() {
  const app = createApp({
    id: "admin",
    routeTree,
    session,
    i18n,
    permissions: async () => (await me()).permissions,
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
