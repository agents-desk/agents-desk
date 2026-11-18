import { createStart } from "@tanstack/react-start"
import { requestMiddleware } from "@go-tenon/start"

// requestMiddleware 记住页面请求的 cookie 等请求头，服务端渲染调用接口时转交给 Go。
// 页面默认在服务端渲染，搜索引擎可以收录；需要登录的页面在路由上设置 ssr: false。
export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
