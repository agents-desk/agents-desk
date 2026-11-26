import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { ChatView } from "../../../../chat/ChatView"

// 打开的单条聊天（内部聊天或商务沟通）：/t/{slug}/chat/{id}。
export const Route = createFileRoute("/t/$slug/chat/$id")({ component: OpenChat })

const tenantRoute = getRouteApi("/t/$slug")
const chatRoute = getRouteApi("/t/$slug/chat")

function OpenChat() {
  const { slug, id } = Route.useParams()
  const tenant = tenantRoute.useLoaderData()
  const search = chatRoute.useSearch()
  const navigate = useNavigate()

  return (
    <ChatView
      key={id}
      id={id}
      tenantId={tenant.id}
      back={() =>
        void navigate({
          to: "/t/$slug/chat",
          params: { slug },
          search: { scope: search.scope, filter: search.filter, channel: search.channel },
        })
      }
      onOpenChat={(nextId) =>
        void navigate({
          to: "/t/$slug/chat/$id",
          params: { slug, id: nextId },
          search: { scope: undefined, filter: undefined, channel: undefined },
        })
      }
    />
  )
}
