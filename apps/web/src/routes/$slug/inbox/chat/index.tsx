import { Button } from "@mantine/core"
import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { IconBriefcase, IconMessageCircle, IconPlus } from "@tabler/icons-react"
import { EmptyState, useCan, useT } from "@go-tenon/ui"
import { useState } from "react"
import { NewChatModal } from "../../../../chat/NewChatModal"

const chatRoute = getRouteApi("/$slug/inbox/chat")

// 尚未打开聊天时的空态引导页：区分内部聊天与商务沟通。
export const Route = createFileRoute("/$slug/inbox/chat/")({ component: NoChat })

function NoChat() {
  const t = useT()
  const can = useCan()
  const { slug } = Route.useParams()
  const search = chatRoute.useSearch()
  const navigate = useNavigate()
  const [newOpen, setNewOpen] = useState(false)
  const isExternal = search.scope === "external"

  if (isExternal) {
    return (
      <EmptyState
        fill
        icon={<IconBriefcase size={24} stroke={1.6} />}
        title={t("chat.business_pick")}
        description={t("chat.business_pick_hint")}
        action={
          can("business-channels:use") ? (
            <Button size="xs" variant="light" onClick={() => void navigate({ to: "/$slug/settings/business-channels", params: { slug } })}>
              {t("chat.business_manage")}
            </Button>
          ) : undefined
        }
      />
    )
  }

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
        onOpened={(chat) => void navigate({ to: "/$slug/inbox/chat/$id", params: { slug, id: chat.id } })}
      />
    </>
  )
}
