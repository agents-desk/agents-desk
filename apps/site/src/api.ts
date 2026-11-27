// 注意：src/client.ts 和 src/server.ts 是 TanStack Start 约定的入口文件名，API 客户端放在 src/api.ts。
// 官网通过同源 __Host-session Cookie 读取 /api/me 感知成员登录状态，用于顶栏在“登录”与“进入工作台”间切换。
import { createSession } from "@go-tenon/client"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@agents-desk/client/api"
import { createKit } from "@agents-desk/client/api/kit"
import { i18n } from "./i18n"

export const session = createSession({
  load: async () => {
    const me = await api.auth.me()
    return { account: me.id }
  },
})

export const api = createClient({ baseURL: "/api", fetch: apiFetch, session, headers: i18n.headers })

export const kit = createKit(api, { scope: session.scope, language: i18n.language })
