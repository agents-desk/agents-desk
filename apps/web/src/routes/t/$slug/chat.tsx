import { Box } from "@mantine/core"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { Pane, useNarrow } from "@go-tenon/ui"
import { useCallback, useState } from "react"
import "../../../inbox/inbox.css"
import type { ChatModeFilter } from "../../../chat/data"
import { ChatList } from "../../../chat/List"
import { ChatNav } from "../../../chat/Nav"
import { NewChatModal } from "../../../chat/NewChatModal"
import { NARROW_QUERY } from "../../../layout"

/** ChatSearch 是聊天模块的路由查询参数，决定二级导航栏高亮与列表筛选模式。 */
export interface ChatSearch {
  filter?: Exclude<ChatModeFilter, "all">
}

// 内部聊天模块：一级图标栏独立入口，二级导航提供全部/单聊/群聊分组，主区为列表与对话区。
export const Route = createFileRoute("/t/$slug/chat")({
  validateSearch: (raw: Record<string, unknown>): ChatSearch => ({
    filter: raw.filter === "direct" || raw.filter === "group" ? raw.filter : undefined,
  }),
  staticData: { title: "nav.chat", icon: "chat", order: 2, permission: "conversations:read", fullBleed: true },
  component: ChatLayout,
})

function ChatLayout() {
  const { slug } = Route.useParams()
  const search = Route.useSearch()
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()
  const narrow = useNarrow(NARROW_QUERY)
  const [newOpen, setNewOpen] = useState(false)

  const filter: ChatModeFilter = search.filter ?? "all"

  const selectFilter = useCallback(
    (next: ChatModeFilter) => {
      void navigate({
        to: id ? "/t/$slug/chat/$id" : "/t/$slug/chat",
        params: id ? { slug, id } : { slug },
        search: { filter: next === "all" ? undefined : next },
      })
    },
    [id, navigate, slug],
  )

  const open = useCallback(
    (nextId: string) =>
      void navigate({
        to: "/t/$slug/chat/$id",
        params: { slug, id: nextId },
        search: { filter: search.filter },
      }),
    [navigate, search.filter, slug],
  )

  return (
    <Box style={{ display: "flex", height: "100%" }}>
      <ChatNav filter={filter} onSelect={selectFilter} />
      {(!narrow || !id) && (
        <Pane
          width={narrow ? "100%" : undefined}
          border={narrow ? "none" : "right"}
          resizable={!narrow && { storageKey: "chat-list", defaultWidth: 300, min: 250, max: 400, side: "right" }}
        >
          <ChatList mode={filter} openId={id} open={open} onNewChat={() => setNewOpen(true)} />
        </Pane>
      )}
      {(!narrow || id) && (
        <Box style={{ flex: 1, minWidth: 0, display: "flex" }}>
          <Outlet />
        </Box>
      )}
      <NewChatModal opened={newOpen} onClose={() => setNewOpen(false)} onOpened={(chat) => open(chat.id)} />
    </Box>
  )
}
