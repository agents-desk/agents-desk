import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { TicketView } from "../../../../tickets/TicketView"

// 打开的单张工单：/t/{slug}/tickets/{id}，列表的分组、状态和类型筛选保存在地址参数中。
export const Route = createFileRoute("/t/$slug/tickets/$id")({ component: OpenTicket })

const ticketsRoute = getRouteApi("/t/$slug/tickets")
const tenantRoute = getRouteApi("/t/$slug")

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
      back={() => void navigate({ to: "/t/$slug/tickets", params: { slug }, search })}
    />
  )
}
