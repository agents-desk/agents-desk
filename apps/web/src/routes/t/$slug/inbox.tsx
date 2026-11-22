import { Box } from "@mantine/core"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { useNarrow } from "@go-tenon/ui"
import { useCallback, useEffect } from "react"
import "../../../inbox/inbox.css"
import { flatten, flattenSearchHits, useConversationList, useConversationSearch } from "../../../inbox/data"
import { ConversationList, type OpenFocus } from "../../../inbox/List"
import { InboxNav } from "../../../inbox/Nav"
import { type InboxSearch, type MatchField, validateInboxSearch } from "../../../inbox/search"
import { isTypingTarget, NARROW_QUERY, ResizeHandle, useResizableWidth } from "../../../layout"

// 收件箱：坐席接待会话的地方，铺满内容区（fullBleed），见产品设计的 13.2。二级导航是带数量的收件箱列表（nav 插槽），
// 右侧是会话列表和会话（子路由 inbox/$id）；当前的列表、状态和排序在地址参数中，切换会话时保留。
// 宽屏支持拖拽与键盘调整列表栏宽；非输入态支持 J/K 快速切换上下会话；窄屏（小于 900px）时会话列表和会话轮流占满。
export const Route = createFileRoute("/t/$slug/inbox")({
  staticData: { title: "nav.inbox", icon: "inbox", order: 1, permission: "conversations:read", fullBleed: true },
  validateSearch: validateInboxSearch,
  component: Inbox,
})

function Inbox() {
  const search = Route.useSearch()
  const { slug } = Route.useParams()
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()
  const narrow = useNarrow(NARROW_QUERY)
  const listSize = useResizableWidth("agents-desk:inbox-list-w", 320, 260, 420, "right")
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
        <Box w={narrow ? "100%" : listSize.width} pos="relative" style={{ flex: "none", minWidth: 0 }}>
          <ConversationList search={search} openId={id} go={go} open={open} />
          {!narrow && (
            <ResizeHandle
              side="right"
              dragging={listSize.dragging}
              width={listSize.width}
              min={listSize.min}
              max={listSize.max}
              onPointerDown={listSize.onPointerDown}
              onDoubleClick={listSize.onDoubleClick}
              onKeyDown={listSize.onKeyDown}
            />
          )}
        </Box>
      )}
      {(!narrow || id) && (
        <Box style={{ flex: 1, minWidth: 0, display: "flex" }}>
          <Outlet />
        </Box>
      )}
    </Box>
  )
}
