import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { ChatView } from "../../../../chat/ChatView"

// 打开的单条内部聊天：/t/{slug}/chat/{id}。
export const Route = createFileRoute("/t/$slug/chat/$id")({ component: OpenChat })

const tenantRoute = getRouteApi("/t/$slug")

function OpenChat() {
  const { slug, id } = Route.useParams()
  const tenant = tenantRoute.useLoaderData()
  const navigate = useNavigate()

  return (
    <ChatView
      key={id}
      id={id}
      tenantId={tenant.id}
      back={() => void navigate({ to: "/t/$slug/chat", params: { slug } })}
    />
  )
}
