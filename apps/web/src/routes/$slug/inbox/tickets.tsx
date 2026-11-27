import { Box } from "@mantine/core"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { Pane, preloadQueries, useNarrow } from "@go-tenon/ui"
import { useCallback, useState } from "react"
import { NARROW_QUERY } from "../../../layout"
import { CreateTicketModal } from "../../../tickets/CreateTicketModal"
import { TicketList } from "../../../tickets/List"
import { type TicketSearch, validateTicketSearch } from "../../../tickets/search"
import { ticketListQuery } from "../../../tickets/meta"

// 收件箱的工单分组：/inbox/tickets，二级导航在上级的收件箱模块中；这里是工单列表和工单（子路由 $id）。
export const Route = createFileRoute("/$slug/inbox/tickets")({
  validateSearch: validateTicketSearch,
  // 切换分组和筛选时预取工单列表，最多等一小会儿：数据很快到时新列表一次完整出现，不先闪成骨架
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => preloadQueries(context.app.queryClient, [ticketListQuery(deps)]),
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
      ? navigate({ to: "/$slug/inbox/tickets/$id", params: { slug, id }, search: next })
      : navigate({ to: "/$slug/inbox/tickets", params: { slug }, search: next }))
  }

  const open = useCallback(
    (nextId: string) =>
      void navigate({
        to: "/$slug/inbox/tickets/$id",
        params: { slug, id: nextId },
        search,
      }),
    [navigate, search, slug],
  )

  return (
    <Box style={{ display: "flex", height: "100%" }}>
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
