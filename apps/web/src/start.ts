import { createStart } from "@tanstack/react-start"
import { requestMiddleware } from "@go-tenon/start"

// requestMiddleware 记住页面请求的 cookie 等请求头。
// 工作区端当前仅在浏览器中渲染（ssr: false）；需要调整登录与页面行为时在各路由设置。
export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
