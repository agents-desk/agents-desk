import { Box } from "@mantine/core"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { Pane, useNarrow } from "@go-tenon/ui"
import { useCallback, useState } from "react"
import "../../../inbox/inbox.css"
import { ChatList } from "../../../chat/List"
import { NewChatModal } from "../../../chat/NewChatModal"
import { NARROW_QUERY } from "../../../layout"

// 内部聊天模块：一级图标栏独立入口，左侧为单聊/群聊列表，右侧为当前聊天对话区。
export const Route = createFileRoute("/t/$slug/chat")({
  staticData: { title: "nav.chat", icon: "chat", order: 2, permission: "conversations:read", fullBleed: true },
  component: ChatLayout,
})

function ChatLayout() {
  const { slug } = Route.useParams()
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()
  const narrow = useNarrow(NARROW_QUERY)
  const [newOpen, setNewOpen] = useState(false)

  const open = useCallback(
    (nextId: string) => void navigate({ to: "/t/$slug/chat/$id", params: { slug, id: nextId } }),
    [navigate, slug],
  )

  return (
    <Box style={{ display: "flex", height: "100%" }}>
      {(!narrow || !id) && (
        <Pane
          width={narrow ? "100%" : undefined}
          border={narrow ? "none" : "right"}
          resizable={!narrow && { storageKey: "chat-list", defaultWidth: 300, min: 250, max: 400, side: "right" }}
        >
          <ChatList openId={id} open={open} onNewChat={() => setNewOpen(true)} />
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
