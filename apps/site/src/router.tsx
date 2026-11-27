import { host } from "@go-tenon/host"
import { cspNonce } from "@go-tenon/start"
import { createApp, NotFoundPage, RouteError } from "@go-tenon/ui"
import { session } from "./api"
import { i18n } from "./i18n"
import { bindObserve, reportRouteError } from "./observe"
import { routeTree } from "./routeTree.gen"

// getRouter 创建官网应用：路由、TanStack Query、登录态感知与界面文字。
export function getRouter() {
  const app = createApp({
    id: "site",
    routeTree,
    session,
    i18n,
    onError: reportRouteError,
    router: { ssr: { nonce: cspNonce() }, defaultErrorComponent: RouteError, defaultNotFoundComponent: NotFoundPage },
  })
  bindObserve(app)
  host()?.on("navigate", (path) => app.router.history.push(path))
  return app.router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
