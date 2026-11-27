import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { ConversationView } from "../../../../inbox/Conversation"
import { flatten, conversationQueries, useConversationList } from "../../../../inbox/data"
import { preloadQueries } from "@go-tenon/ui"

// 打开的会话：/app/{slug}/inbox/{id}，收件箱的列表、状态和排序在地址参数中保留。
// loader 预取会话和时间线，最多等一小会儿：数据很快到时新会话一次完整出现，不先闪成加载状态。
export const Route = createFileRoute("/$slug/inbox/_conversations/$id")({
  loaderDeps: ({ search }) => ({ seq: search.seq }),
  loader: ({ context, params, deps }) => preloadQueries(context.app.queryClient, conversationQueries(context.app.queryClient, params.id, deps.seq)),
  component: OpenConversation,
})

const inbox = getRouteApi("/$slug/inbox/_conversations")
const tenantRoute = getRouteApi("/$slug")

function OpenConversation() {
  const { slug, id } = Route.useParams()
  const search = inbox.useSearch()
  const tenant = tenantRoute.useLoaderData()
  const navigate = useNavigate()
  const list = useConversationList(search)
  const open = (next: string) => void navigate({ to: "/$slug/inbox/$id", params: { slug, id: next }, search })
  // 下一个待回复：当前列表中排在这个会话后面、正在等待回复的第一个；后面没有时从头找
  const items = flatten(list.data?.pages)
  const at = items.findIndex((c) => c.id === id)
  const waiting = [...items.slice(at + 1), ...items.slice(0, Math.max(at, 0))].find((c) => c.waiting_since && c.id !== id)
  return (
    <ConversationView
      key={id}
      id={id}
      tenantId={tenant.id}
      highlight={search.q}
      focusSeq={search.seq}
      focusField={search.matchField}
      open={open}
      next={waiting ? () => open(waiting.id) : undefined}
      back={() => void navigate({ to: "/$slug/inbox", params: { slug }, search })}
    />
  )
}
