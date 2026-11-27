import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { ContactDetailPanel, contactQueries } from "../../../contacts/Detail"
import { preloadQueries } from "@go-tenon/ui"

// 右侧分栏中打开的联系人档案：/app/{slug}/contacts/{id}，列表的筛选和页码保留在地址参数中。
// loader 预取档案和会话，最多等一小会儿：数据很快到时新档案一次完整出现，不先闪成加载状态。
export const Route = createFileRoute("/$slug/contacts/$id")({
  loader: ({ context, params }) => preloadQueries(context.app.queryClient, contactQueries(params.id)),
  component: OpenContact,
})

const contactsRoute = getRouteApi("/$slug/contacts")

function OpenContact() {
  const { slug, id } = Route.useParams()
  const search = contactsRoute.useSearch()
  const navigate = useNavigate()
  return (
    <ContactDetailPanel
      key={id}
      slug={slug}
      id={id}
      onClose={() => void navigate({ to: "/$slug/contacts", params: { slug }, search })}
    />
  )
}
