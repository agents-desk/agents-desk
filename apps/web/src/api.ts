// 注意：src/client.ts 和 src/server.ts 是 TanStack Start 约定的入口文件名，API 客户端放在 src/api.ts。
// 工作区端调用两个 API，都是 tenon generate 生成的客户端，接口改了以后 tenon dev 会自动重新生成：
//   - api 是 /api：成员的登录、退出、当前账号和所属的工作区（packages/client/src/api）；
//   - tenantApi 是 /t/{slug}/api：当前工作区的接口（packages/client/src/tenant），slug 取自会话的身份。
// 登录凭据是 HttpOnly 的会话 cookie __Host-session，浏览器随同源请求自动带上，页面上的脚本不接触它。
import { createSession } from "@go-tenon/client"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@agents-desk/client/api"
import { createKit as createApiKit } from "@agents-desk/client/api/kit"
import { createClient as createTenantClient } from "@agents-desk/client/tenant"
import { createKit } from "@agents-desk/client/tenant/kit"
import { i18n } from "./i18n"

// session 记录当前的成员和成员所在的工作区：打开页面、登录后读取 /api/me，没有登录时它返回 401。读取当前成员时界面切换到成员的界面语言，退出后回到浏览器的语言。
// 身份中的 tenant 是工作区的 slug，进入 /app/{slug}/ 时由 enterTenant 设置。受保护的接口返回 401 时，会话随之变为未登录。
export const session = createSession({
  load: async () => {
    const me = await api.auth.me()
    i18n.setLanguage(me.ui_language || me.language)
    return { account: me.id }
  },
})
session.subscribe(() => {
  if (!session.signedIn()) i18n.setLanguage(null)
})

// 请求带上当前语言（Accept-Language），服务端的错误提示与界面是同一种语言。
export const api = createClient({ baseURL: "/api", fetch: apiFetch, session, headers: i18n.headers })

export const tenantApi = createTenantClient({
  baseURL: () => `/t/${session.identity()?.tenant ?? ""}/api`,
  fetch: apiFetch,
  session,
  headers: i18n.headers,
})

// kit 是工作区 API 生成的元数据：查询选项、校验规则、枚举的显示文字和资源。scope 让缓存按成员和工作区区分，
// 显示文字按界面的当前语言。
export const kit = createKit(tenantApi, { scope: session.scope, language: i18n.language })

// apiKit 是 /api 生成的元数据，例如成员的语言（MemberLanguage）的显示文字。
export const apiKit = createApiKit(api, { scope: session.scope, language: i18n.language })
