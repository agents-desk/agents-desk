import { Button } from "@mantine/core"
import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { IconPlus, IconTicket } from "@tabler/icons-react"
import { EmptyState, useCan, useT } from "@go-tenon/ui"
import { useState } from "react"
import { CreateTicketModal } from "../../../../tickets/CreateTicketModal"

// 还没有打开工单时的空态引导页。
export const Route = createFileRoute("/$slug/inbox/tickets/")({ component: NoTicket })

const ticketsRoute = getRouteApi("/$slug/inbox/tickets")

function NoTicket() {
  const t = useT()
  const can = useCan()
  const { slug } = Route.useParams()
  const search = ticketsRoute.useSearch()
  const navigate = useNavigate()
  const [createOpen, setCreateOpen] = useState(false)

  return (
    <>
      <EmptyState
        fill
        icon={<IconTicket size={24} stroke={1.6} />}
        title={t("tickets.pick")}
        description={t("tickets.pick_hint")}
        action={
          can("conversations:reply") && (
            <Button size="xs" variant="light" leftSection={<IconPlus size={14} />} onClick={() => setCreateOpen(true)}>
              {t("tickets.create")}
            </Button>
          )
        }
      />
      <CreateTicketModal
        opened={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(created) =>
          void navigate({ to: "/$slug/inbox/tickets/$id", params: { slug, id: created.id }, search })
        }
      />
    </>
  )
}
