import { useQuery } from "@tanstack/react-query"
import { type App, type LanguageOption, useApp } from "@go-tenon/ui"
import { api } from "./api"

/** UI_LANGUAGES 是工作台支持的界面语言选项，使用各语言自身名称供登录页与选区页切换。 */
export const UI_LANGUAGES: LanguageOption[] = [
  { value: "zh-CN", label: "简体中文" }, // i18n-ignore
  { value: "en", label: "English" },
]

/** lastWorkspaceKey 返回按账号隔离的上次访问工作区存储键。 */
function lastWorkspaceKey(accountId: string): string {
  return `last-workspace:${accountId}`
}

/** readLastWorkspace 读取账号最近一次访问的工作区 slug，没有记录时返回 undefined。 */
export function readLastWorkspace(app: App, accountId?: string): string | undefined {
  if (!accountId) return undefined
  const slug = app.storage.get<string>(lastWorkspaceKey(accountId))
  return typeof slug === "string" && slug.length > 0 ? slug : undefined
}

/** writeLastWorkspace 记录账号最近一次访问的工作区 slug，供下次打开 /app/ 时直达。 */
export function writeLastWorkspace(app: App, accountId: string | undefined, slug: string): void {
  if (!accountId || !slug) return
  app.storage.set(lastWorkspaceKey(accountId), slug)
}

/** workspacesQuery 返回读取当前登录账号所属工作区列表的查询选项。 */
export function workspacesQuery(accountId?: string) {
  return {
    queryKey: ["workspaces", "mine", accountId ?? ""] as const,
    queryFn: () => api.tenant.mine(),
    staleTime: 60_000,
  }
}

/** useWorkspaces 读取当前登录成员所属的全部工作区（含 Logo、状态和地址），并拆分活跃列表。 */
export function useWorkspaces() {
  const app = useApp()
  const accountId = app.session.identity()?.account
  const query = useQuery({
    ...workspacesQuery(accountId),
    enabled: app.signedIn(),
  })
  const items = query.data ?? []
  const active = items.filter((w) => w.status === "active")
  return {
    ...query,
    items,
    active,
  }
}
