import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { ContactDetailPanel } from "../../../../contacts/Detail"

// 右侧分栏中打开的联系人档案：/t/{slug}/contacts/{id}，列表的筛选和页码保留在地址参数中。
export const Route = createFileRoute("/t/$slug/contacts/$id")({
  component: OpenContact,
})

const contactsRoute = getRouteApi("/t/$slug/contacts")

function OpenContact() {
  const { slug, id } = Route.useParams()
  const search = contactsRoute.useSearch()
  const navigate = useNavigate()
  return (
    <ContactDetailPanel
      key={id}
      slug={slug}
      id={id}
      onClose={() => void navigate({ to: "/t/$slug/contacts", params: { slug }, search })}
    />
  )
}
