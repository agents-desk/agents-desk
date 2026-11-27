import { Button } from "@mantine/core"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconMessageCircle, IconPlus } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import { useState } from "react"
import { NewChatModal } from "../../../chat/NewChatModal"
import { EmptyState } from "../../../components/empty-state"

// 尚未打开聊天时的空态引导页。
export const Route = createFileRoute("/$slug/chat/")({ component: NoChat })

function NoChat() {
  const t = useT()
  const { slug } = Route.useParams()
  const navigate = useNavigate()
  const [newOpen, setNewOpen] = useState(false)

  return (
    <>
      <EmptyState
        fill
        icon={<IconMessageCircle size={24} stroke={1.6} />}
        title={t("chat.pick")}
        description={t("chat.pick_hint")}
        action={
          <Button size="xs" variant="light" leftSection={<IconPlus size={14} />} onClick={() => setNewOpen(true)}>
            {t("chat.new")}
          </Button>
        }
      />
      <NewChatModal
        opened={newOpen}
        onClose={() => setNewOpen(false)}
        onOpened={(chat) => void navigate({ to: "/$slug/chat/$id", params: { slug, id: chat.id } })}
      />
    </>
  )
}
