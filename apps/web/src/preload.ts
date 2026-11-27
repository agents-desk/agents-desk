import type { QueryClient, QueryKey } from "@tanstack/react-query"

// 打开详情时用列表中已有的那一项作占位：顶栏、输入框、详情栏不用等请求。
// 路由 loader 的预取用 @go-tenon/ui 的 preloadQueries，列表行悬停、J/K 切换的预取用 prefetchQueries。

/** findInLists 在 prefix 之下已缓存的游标分页列表中找到 id 那一项，用作打开详情时的占位数据。 */
export function findInLists<T extends { id: string }>(queryClient: QueryClient, prefix: QueryKey, id: string): T | undefined {
  for (const [, data] of queryClient.getQueriesData<{ pages?: { items?: T[] }[] }>({ queryKey: prefix })) {
    for (const page of data?.pages ?? []) {
      const hit = page.items?.find((x) => x.id === id)
      if (hit) return hit
    }
  }
  return undefined
}
