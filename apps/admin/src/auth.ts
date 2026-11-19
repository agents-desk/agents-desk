import { host } from "@go-tenon/host"
import { api, session } from "./api"

// 平台管理员的登录、退出和当前账号，调用生成的客户端（src/api.ts）。

export { session }

/** 当前登录的平台管理员：名字、邮箱和平台的权限。 */
export const me = () => api.auth.me()

export const login = (values: { email: string; password: string }) => api.auth.login(values)

// 退出：撤销这次登录；在桌面端中再让宿主删除保存的 token。
export async function logout(): Promise<void> {
  await api.auth.logout()
  await host()?.auth.signOut()
}
