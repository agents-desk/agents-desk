import { Box } from "@mantine/core"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { useNarrow } from "@go-tenon/ui"
import "../../../inbox/inbox.css"
import { ConversationList } from "../../../inbox/List"
import { InboxNav } from "../../../inbox/Nav"
import { type InboxSearch, validateInboxSearch } from "../../../inbox/search"
import { NARROW_QUERY, ResizeHandle, useResizableWidth } from "../../../layout"

// 收件箱：坐席接待会话的地方，铺满内容区（fullBleed），见产品设计的 13.2。二级导航是带数量的收件箱列表（nav 插槽），
// 右侧是会话列表和会话（子路由 inbox/$id）；当前的列表、状态和排序在地址参数中，切换会话时保留。
// 宽屏支持拖拽调整列表栏宽；窄屏（小于 900px）时会话列表和会话轮流占满：打开了会话就只显示会话。
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
  // go 切换列表、状态或排序：打开着的会话保持打开
  const go = (patch: Partial<InboxSearch>) => {
    const next = { ...search, ...patch }
    void (id
      ? navigate({ to: "/t/$slug/inbox/$id", params: { slug, id }, search: next })
      : navigate({ to: "/t/$slug/inbox", params: { slug }, search: next }))
  }
  const open = (next: string) => void navigate({ to: "/t/$slug/inbox/$id", params: { slug, id: next }, search })
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
              onPointerDown={listSize.onPointerDown}
              onDoubleClick={listSize.onDoubleClick}
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
