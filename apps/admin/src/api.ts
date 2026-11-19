// 注意：src/client.ts 和 src/server.ts 是 TanStack Start 约定的入口文件名，API 客户端放在 src/api.ts。
// api 是 tenon generate 生成的 /admin/api 客户端（packages/client/src/admin），接口改了以后 tenon dev 会自动重新生成。
// 管理员端只接受平台管理员，登录凭据是 HttpOnly 的会话 cookie __Host-admin，浏览器随同源请求自动带上，页面上的脚本不接触它。
// kit 是同一个 API 生成的元数据：查询选项、校验规则、验证提示、枚举的显示文字和资源。
import { createSession } from "@go-tenon/client"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@agents-desk/client/admin"
import { createKit } from "@agents-desk/client/admin/kit"

// session 记录当前的平台管理员：打开页面、登录后读取 /admin/api/me，没有登录时它返回 401。
// 受保护的接口返回 401 时，会话随之变为未登录。
export const session = createSession({ load: async () => ({ account: (await api.auth.me()).id }) })

export const api = createClient({ baseURL: "/admin/api", fetch: apiFetch, session })

// scope 让查询的缓存按登录的账号区分。
export const kit = createKit(api, { scope: session.scope, language: "zh-CN" })
