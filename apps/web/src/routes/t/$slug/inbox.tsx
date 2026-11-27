import { Box } from "@mantine/core"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { Pane, useNarrow } from "@go-tenon/ui"
import { useCallback, useEffect } from "react"
import "../../../inbox/inbox.css"
import { conversationListQuery, flatten, flattenSearchHits, prefetchConversation, useConversationList, useConversationSearch } from "../../../inbox/data"
import { ConversationList, type OpenFocus } from "../../../inbox/List"
import { InboxNav } from "../../../inbox/Nav"
import { type InboxSearch, type MatchField, validateInboxSearch } from "../../../inbox/search"
import { isTypingTarget, NARROW_QUERY } from "../../../layout"
import { useQueryClient } from "@tanstack/react-query"
import { warmRoute } from "../../../preload"

// 收件箱：坐席接待会话的地方，铺满内容区（fullBleed），见产品设计的 13.2。二级导航是带数量的收件箱列表（nav 插槽），
// 右侧是会话列表和会话（子路由 inbox/$id）；当前的列表、状态和排序在地址参数中，切换会话时保留。
// 宽屏支持拖拽与键盘调整列表栏宽；非输入态支持 J/K 快速切换上下会话；窄屏（小于 900px）时会话列表和会话轮流占满。
export const Route = createFileRoute("/t/$slug/inbox")({
  staticData: { title: "nav.inbox", icon: "inbox", order: 1, permission: "conversations:read", fullBleed: true },
  validateSearch: validateInboxSearch,
  // 切换列表、状态和排序时预取会话列表，最多等一小会儿：数据很快到时新列表一次完整出现，不先闪成骨架
  loaderDeps: ({ search }) => ({
    box: search.box,
    team: search.team,
    view: search.view,
    filter: search.filter,
    tag: search.tag,
    status: search.status,
    sort: search.sort,
    q: search.q,
  }),
  loader: ({ context, deps }) => (deps.q?.trim() ? undefined : warmRoute(context.app.queryClient, [conversationListQuery(deps)])),
  component: Inbox,
})

function Inbox() {
  const search = Route.useSearch()
  const { slug } = Route.useParams()
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()
  const narrow = useNarrow(NARROW_QUERY)
  const list = useConversationList(search)
  const searchQuery = useConversationSearch(search)
  const searching = Boolean(search.q?.trim())
  // go 切换列表、状态或排序：打开着的会话保持打开
  const go = (patch: Partial<InboxSearch>) => {
    const next = { ...search, ...patch }
    void (id
      ? navigate({ to: "/t/$slug/inbox/$id", params: { slug, id }, search: next })
      : navigate({ to: "/t/$slug/inbox", params: { slug }, search: next }))
  }
  const open = useCallback(
    (next: string, focus?: OpenFocus) =>
      void navigate({
        to: "/t/$slug/inbox/$id",
        params: { slug, id: next },
        search: { ...search, seq: focus?.seq, matchField: focus?.matchField },
      }),
    [navigate, search, slug],
  )

  // 打开一条会话后预取列表中它的上一条和下一条：按 J/K 时直接显示
  const queryClient = useQueryClient()
  useEffect(() => {
    if (!id || searching) return
    const items = flatten(list.data?.pages)
    const at = items.findIndex((c) => c.id === id)
    if (at < 0) return
    for (const c of [items[at + 1], items[at - 1]]) if (c) void prefetchConversation(queryClient, c.id)
  }, [id, list.data?.pages, queryClient, searching])

  // 非输入态按 J/K 切换当前列表或搜索结果中的下一条/上一条会话
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return
      const key = e.key.toLowerCase()
      if (key !== "j" && key !== "k") return
      if (searching) {
        const hits = flattenSearchHits(searchQuery.data?.pages)
        if (hits.length === 0) return
        e.preventDefault()
        const at = id ? hits.findIndex((h) => h.conversation.id === id) : -1
        const nextIndex = key === "j" ? (at < 0 ? 0 : Math.min(hits.length - 1, at + 1)) : at <= 0 ? 0 : at - 1
        const target = hits[nextIndex]
        if (target && target.conversation.id !== id) {
          open(target.conversation.id, { seq: target.match.seq, matchField: target.match.field as MatchField })
        }
        return
      }
      const items = flatten(list.data?.pages)
      if (items.length === 0) return
      e.preventDefault()
      const at = id ? items.findIndex((c) => c.id === id) : -1
      const nextIndex = key === "j" ? (at < 0 ? 0 : Math.min(items.length - 1, at + 1)) : at <= 0 ? 0 : at - 1
      const target = items[nextIndex]
      if (target && target.id !== id) open(target.id)
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [id, list.data?.pages, open, searchQuery.data?.pages, searching])
  return (
    <Box style={{ display: "flex", height: "100%" }}>
      <InboxNav search={search} go={go} />
      {(!narrow || !id) && (
        <Pane
          width={narrow ? "100%" : undefined}
          border={narrow ? "none" : "right"}
          resizable={!narrow && { storageKey: "inbox-list", defaultWidth: 320, min: 260, max: 420, side: "right" }}
        >
          <ConversationList search={search} openId={id} go={go} open={open} />
        </Pane>
      )}
      {(!narrow || id) && (
        <Box style={{ flex: 1, minWidth: 0, display: "flex" }}>
          <Outlet />
        </Box>
      )}
    </Box>
  )
}
