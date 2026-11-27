import type { FetchInfiniteQueryOptions, FetchQueryOptions, QueryClient, QueryKey } from "@tanstack/react-query"

// 切换页面和打开详情时不闪烁：
// - 路由的 loader 预取页面的数据并最多等一小会儿（waitBriefly），数据很快到时新页面一次完整出现，旧页面一直留到那时；
// - 列表行悬停、J/K 切换时提前预取（prefetch），点下去时数据通常已经在缓存中；
// - 打开详情时用列表中已有的那一项作占位（findInLists），顶栏、输入框、详情栏不用等请求。

/** PREFETCH_FRESH 是预取时认为数据还新的时长：在列表上来回移动鼠标不会反复请求同一项。 */
const PREFETCH_FRESH = 30_000

/** Warmable 是可以预取的查询：普通查询或游标分页（infinite）查询的选项。 */
// biome-ignore lint/suspicious/noExplicitAny: 各页面的查询返回不同的类型
export type Warmable = FetchQueryOptions<any, any, any, any> | FetchInfiniteQueryOptions<any, any, any, any, any>

/** prefetch 预取一组查询；失败时不抛错，页面自己的查询会重试并显示错误。 */
export function prefetch(queryClient: QueryClient, queries: Warmable[]): Promise<void> {
  return Promise.all(
    queries.map((q) =>
      "initialPageParam" in q
        ? queryClient.prefetchInfiniteQuery({ ...(q as FetchInfiniteQueryOptions<any, any, any, any, any>), staleTime: PREFETCH_FRESH })
        : queryClient.prefetchQuery({ ...(q as FetchQueryOptions<any, any, any, any>), staleTime: PREFETCH_FRESH }),
    ),
  ).then(() => undefined)
}

/**
 * warmRoute 是路由 loader 用的预取：缓存中已经都有时在后台刷新、立即放行（打开会话不会因为刷新列表而等待）；
 * 还没有时预取并最多等 WAIT_MS，让新页面一次完整出现。
 */
export function warmRoute(queryClient: QueryClient, queries: Warmable[]): Promise<void> {
  const cached = queries.every((q) => queryClient.getQueryData(q.queryKey) !== undefined)
  const work = prefetch(queryClient, queries)
  return cached ? Promise.resolve() : waitBriefly(work)
}

/** WAIT_MS 是路由 loader 等预取数据的上限：本机和线上常见的请求在这之内完成，更慢时先切换、页面显示占位和骨架。 */
const WAIT_MS = 250

/** waitBriefly 等 work 完成，最多等 WAIT_MS。路由的 loader 返回它。 */
function waitBriefly(work: Promise<unknown>): Promise<void> {
  return Promise.race([work.then(() => undefined), new Promise<void>((resolve) => setTimeout(resolve, WAIT_MS))])
}

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
