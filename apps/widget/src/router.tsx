import { cspNonce } from "@go-tenon/start"
import { createApp, NotFoundPage, RouteError } from "@go-tenon/ui"
import { i18n } from "./i18n"
import { routeTree } from "./routeTree.gen"

// getRouter 创建应用：路由、TanStack Query、界面文字，见框架文档的“应用骨架”。
// 服务端渲染时每个请求调用一次，所以每次都创建新的应用。
export function getRouter() {
  const app = createApp({
    id: "widget",
    routeTree,
    i18n,
    router: { ssr: { nonce: cspNonce() }, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  })
  return app.router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
