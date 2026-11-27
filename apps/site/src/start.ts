import { createStart } from "@tanstack/react-start"
import { requestMiddleware } from "@go-tenon/start"

// requestMiddleware 记住页面请求的 cookie 等请求头，服务端渲染调用接口时转交给 Go。
// 页面只在浏览器中渲染（package.json 的 tenon.ssr 是 false），后台不需要搜索引擎收录。
export const startInstance = createStart(() => ({ requestMiddleware: [requestMiddleware] }))
