import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { TicketView } from "../../../../tickets/TicketView"
import { ticketQueries } from "../../../../tickets/meta"
import { preloadQueries } from "@go-tenon/ui"

// 打开的单张工单：/app/{slug}/inbox/tickets/{id}，列表的分组、状态和类型筛选保存在地址参数中。
// loader 预取工单和时间线，最多等一小会儿：数据很快到时新工单一次完整出现，不先闪成加载状态。
export const Route = createFileRoute("/$slug/inbox/tickets/$id")({
  loader: ({ context, params }) => preloadQueries(context.app.queryClient, ticketQueries(context.app.queryClient, params.id)),
  component: OpenTicket,
})

const ticketsRoute = getRouteApi("/$slug/inbox/tickets")
const tenantRoute = getRouteApi("/$slug")

function OpenTicket() {
  const { slug, id } = Route.useParams()
  const search = ticketsRoute.useSearch()
  const tenant = tenantRoute.useLoaderData()
  const navigate = useNavigate()

  return (
    <TicketView
      key={id}
      id={id}
      tenantId={tenant.id}
      back={() => void navigate({ to: "/$slug/inbox/tickets", params: { slug }, search })}
    />
  )
}
