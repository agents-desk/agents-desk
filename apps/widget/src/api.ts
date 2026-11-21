// 聊天窗口调用的接口：/w/{channel}/api（packages/client/src/widget，tenon generate 生成）。请求用 Authorization: Bearer
// 带上联系人的令牌：聊天窗口在第三方网站的 iframe 中，不用 cookie。令牌由 identity.ts 建立和保存。
import { apiFetch } from "@go-tenon/start"
import { createClient, isApiError } from "@agents-desk/client/widget"
import { channel } from "./host"
import { i18n } from "./i18n"
import { identity } from "./identity"

export const widgetApi = createClient({
  baseURL: `/w/${encodeURIComponent(channel)}/api`,
  fetch: apiFetch,
  headers: () => {
    const token = identity.token()
    return { ...i18n.headers(), Authorization: token ? `Bearer ${token}` : undefined }
  },
})

identity.connect(widgetApi)

/**
 * authed 调用需要联系人令牌的接口 call：令牌失效（401，例如过期、被撤销）时重新建立会话，再调用一次。
 */
export async function authed<T>(call: () => Promise<T>): Promise<T> {
  await identity.ready()
  try {
    return await call()
  } catch (err) {
    if (!isApiError(err) || err.status !== 401) throw err
    await identity.renew()
    return call()
  }
}
