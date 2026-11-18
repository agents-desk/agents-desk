import { createSession } from "@go-tenon/client"
import { host } from "@go-tenon/host"
import { apiFetch } from "@go-tenon/start"
import { createClient } from "@agents-desk/client/admin"

// 管理员端调用 /admin/api：只接受平台管理员，登录凭据是 HttpOnly 的会话 cookie __Host-admin，
// 浏览器随同源请求自动带上，页面上的脚本不接触它。受保护的接口返回 401 时，会话随之变为未登录。

// session 记录当前的平台管理员：打开页面、登录后读取 /admin/api/me，没有登录时它返回 401。
export const session = createSession({ load: async () => ({ account: (await me()).id }) })

// api 是 tenon generate 生成的 /admin/api 客户端（packages/client/src/admin）。
export const api = createClient({ baseURL: "/admin/api", fetch: apiFetch, session })

/** 当前登录的平台管理员：名字、邮箱和平台的权限。 */
export const me = () => api.auth.me()

export const login = (values: { email: string; password: string }) => api.auth.login(values)

// 退出：撤销这次登录；在桌面端中再让宿主删除保存的 token。
export async function logout(): Promise<void> {
  await api.auth.logout()
  await host()?.auth.signOut()
}
