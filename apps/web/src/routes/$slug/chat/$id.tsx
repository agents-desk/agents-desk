import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { ChatView } from "../../../chat/ChatView"
import { chatQueries } from "../../../chat/data"
import { preloadQueries } from "@go-tenon/ui"

// 打开的单条聊天（内部聊天或商务沟通）：/app/{slug}/chat/{id}。
// loader 预取聊天和时间线，最多等一小会儿：数据很快到时新聊天一次完整出现，不先闪成加载状态。
export const Route = createFileRoute("/$slug/chat/$id")({
  loader: ({ context, params }) => preloadQueries(context.app.queryClient, chatQueries(context.app.queryClient, params.id)),
  component: OpenChat,
})

const tenantRoute = getRouteApi("/$slug")
const chatRoute = getRouteApi("/$slug/chat")

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
          to: "/$slug/chat",
          params: { slug },
          search: { scope: search.scope, filter: search.filter, channel: search.channel },
        })
      }
      onOpenChat={(nextId) =>
        void navigate({
          to: "/$slug/chat/$id",
          params: { slug, id: nextId },
          search: { scope: undefined, filter: undefined, channel: undefined },
        })
      }
    />
  )
}
