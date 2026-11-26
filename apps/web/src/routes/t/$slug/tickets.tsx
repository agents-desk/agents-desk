import { Box } from "@mantine/core"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { Pane, useNarrow } from "@go-tenon/ui"
import { useCallback, useState } from "react"
import "../../../inbox/inbox.css"
import { InboxNav } from "../../../inbox/Nav"
import { NARROW_QUERY } from "../../../layout"
import { CreateTicketModal } from "../../../tickets/CreateTicketModal"
import { TicketList } from "../../../tickets/List"
import { type TicketSearch, validateTicketSearch } from "../../../tickets/search"

// 工单工作台：与收件箱共享同一套左侧二级导航（<InboxNav>）和全宽分栏骨架，不单独占一级图标栏。
export const Route = createFileRoute("/t/$slug/tickets")({
  staticData: { permission: "conversations:read", fullBleed: true },
  validateSearch: validateTicketSearch,
  component: TicketsLayout,
})

function TicketsLayout() {
  const search = Route.useSearch()
  const { slug } = Route.useParams()
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()
  const narrow = useNarrow(NARROW_QUERY)
  const [createOpen, setCreateOpen] = useState(false)

  const go = (patch: Partial<TicketSearch>) => {
    const next = { ...search, ...patch }
    void (id
      ? navigate({ to: "/t/$slug/tickets/$id", params: { slug, id }, search: next })
      : navigate({ to: "/t/$slug/tickets", params: { slug }, search: next }))
  }

  const open = useCallback(
    (nextId: string) =>
      void navigate({
        to: "/t/$slug/tickets/$id",
        params: { slug, id: nextId },
        search,
      }),
    [navigate, search, slug],
  )

  return (
    <Box style={{ display: "flex", height: "100%" }}>
      <InboxNav
        activeTicketBox={search.box}
        onSelectTicketBox={(box) =>
          void navigate({ to: "/t/$slug/tickets", params: { slug }, search: { ...search, box } })
        }
        onCreateTicket={() => setCreateOpen(true)}
      />
      {(!narrow || !id) && (
        <Pane
          width={narrow ? "100%" : undefined}
          border={narrow ? "none" : "right"}
          resizable={!narrow && { storageKey: "tickets-list", defaultWidth: 320, min: 260, max: 420, side: "right" }}
        >
          <TicketList search={search} openId={id} go={go} open={open} onCreate={() => setCreateOpen(true)} />
        </Pane>
      )}
      {(!narrow || id) && (
        <Box style={{ flex: 1, minWidth: 0, display: "flex" }}>
          <Outlet />
        </Box>
      )}
      <CreateTicketModal
        opened={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(created) => open(created.id)}
      />
    </Box>
  )
}
