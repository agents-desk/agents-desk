// 注意：src/client.ts 和 src/server.ts 是 TanStack Start 约定的入口文件名，API 客户端放在 src/api.ts。
// 工作区端调用两个 API，都是 tenon generate 生成的客户端，接口改了以后 tenon dev 会自动重新生成：
//   - api 是 /api：成员的登录、退出、当前账号和所属的工作区（packages/client/src/api）；
//   - tenantApi 是 /t/{slug}/api：当前工作区的接口（packages/client/src/tenant），slug 取自会话的身份。
// 登录凭据是 HttpOnly 的会话 cookie __Host-session，浏览器随同源请求自动带上，页面上的脚本不接触它。
import { createSession } from "@go-tenon/client"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@agents-desk/client/api"
import { createClient as createTenantClient } from "@agents-desk/client/tenant"
import { createKit } from "@agents-desk/client/tenant/kit"

// session 记录当前的成员和他所在的工作区：打开页面、登录后读取 /api/me，没有登录时它返回 401。
// 身份中的 tenant 是工作区的 slug，进入 /t/{slug}/ 时由 enterTenant 设置。受保护的接口返回 401 时，会话随之变为未登录。
export const session = createSession({ load: async () => ({ account: (await api.auth.me()).id }) })

export const api = createClient({ baseURL: "/api", fetch: apiFetch, session })

export const tenantApi = createTenantClient({
  baseURL: () => `/t/${session.identity()?.tenant ?? ""}/api`,
  fetch: apiFetch,
  session,
})

// kit 是工作区 API 生成的元数据：查询选项、校验规则、枚举的显示文字和资源。scope 让缓存按成员和工作区区分。
export const kit = createKit(tenantApi, { scope: session.scope, language: "zh-CN" })
