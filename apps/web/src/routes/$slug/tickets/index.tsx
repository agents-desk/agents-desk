import { Button } from "@mantine/core"
import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { IconPlus, IconTicket } from "@tabler/icons-react"
import { useCan, useT } from "@go-tenon/ui"
import { useState } from "react"
import { CreateTicketModal } from "../../../tickets/CreateTicketModal"
import { EmptyState } from "../../../components/empty-state"

// 还没有打开工单时的空态引导页。
export const Route = createFileRoute("/$slug/tickets/")({ component: NoTicket })

const ticketsRoute = getRouteApi("/$slug/tickets")

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
          void navigate({ to: "/$slug/tickets/$id", params: { slug, id: created.id }, search })
        }
      />
    </>
  )
}
