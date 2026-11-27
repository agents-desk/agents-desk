import type { ChatScope } from "@agents-desk/client/tenant"
import { Box } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { Pane, preloadQueries, useCan, useNarrow } from "@go-tenon/ui"
import { useCallback, useMemo, useState } from "react"
import "../../../inbox/inbox.css"
import { kit } from "../../../api"
import { type ChatModeFilter, chatListQuery } from "../../../chat/data"
import { ChatList } from "../../../chat/List"
import { ChatNav, type ChatNavTarget } from "../../../chat/Nav"
import { NewChatModal } from "../../../chat/NewChatModal"
import { NARROW_QUERY } from "../../../layout"

/** ChatSearch 是聊天模块的路由查询参数，决定二级导航栏高亮与列表筛选模式（内部聊天或商务沟通）。 */
export interface ChatSearch {
  scope?: ChatScope
  filter?: Exclude<ChatModeFilter, "all">
  channel?: string
}

// 聊天模块：一级图标栏独立入口，二级导航提供内部聊天（全部/单聊/群聊）与商务沟通（全部/个人渠道），主区为列表与对话区。
export const Route = createFileRoute("/t/$slug/chat")({
  validateSearch: (raw: Record<string, unknown>): ChatSearch => {
    const scope: ChatScope | undefined = raw.scope === "external" || raw.scope === "internal" ? raw.scope : undefined
    const filter: Exclude<ChatModeFilter, "all"> | undefined = raw.filter === "direct" || raw.filter === "group" ? raw.filter : undefined
    const channel = typeof raw.channel === "string" && raw.channel.trim() ? raw.channel.trim() : undefined
    return {
      scope,
      filter: scope === "external" ? undefined : filter,
      channel: scope === "external" ? channel : undefined,
    }
  },
  staticData: { title: "nav.chat", icon: "chat", order: 2, permission: "conversations:read", fullBleed: true },
  // 切换分组和渠道时预取聊天列表，最多等一小会儿：数据很快到时新列表一次完整出现，不先闪成骨架
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => {
    const scope = deps.scope ?? "internal"
    const mode = scope === "external" ? "all" : (deps.filter ?? "all")
    return preloadQueries(context.app.queryClient, [chatListQuery("", mode, scope, scope === "external" ? deps.channel : undefined)])
  },
  component: ChatLayout,
})

function ChatLayout() {
  const { slug } = Route.useParams()
  const search = Route.useSearch()
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()
  const narrow = useNarrow(NARROW_QUERY)
  const [newOpen, setNewOpen] = useState(false)

  const scope: ChatScope = search.scope ?? "internal"
  const filter: ChatModeFilter = scope === "external" ? "all" : (search.filter ?? "all")
  const channelId = scope === "external" ? search.channel : undefined

  const can = useCan()
  const canUseBusiness = can("business-channels:use")
  const businessLinks = useQuery({ ...kit.queries.business.linkList(), enabled: canUseBusiness })
  const tgAccounts = useQuery({ ...kit.queries.tgaccount.list(), enabled: canUseBusiness })
  const channelName = useMemo(() => {
    if (!channelId) return undefined
    const link = businessLinks.data?.find((x) => x.id === channelId)
    if (link) return link.name
    return tgAccounts.data?.accounts.find((x) => x.id === channelId)?.name
  }, [businessLinks.data, channelId, tgAccounts.data?.accounts])

  const selectNav = useCallback(
    (next: ChatNavTarget) => {
      void navigate({
        to: id ? "/t/$slug/chat/$id" : "/t/$slug/chat",
        params: id ? { slug, id } : { slug },
        search: {
          scope: next.scope === "external" ? "external" : undefined,
          filter: next.scope === "internal" ? next.filter : undefined,
          channel: next.scope === "external" ? next.channel : undefined,
        },
      })
    },
    [id, navigate, slug],
  )

  const open = useCallback(
    (nextId: string) =>
      void navigate({
        to: "/t/$slug/chat/$id",
        params: { slug, id: nextId },
        search: {
          scope: search.scope,
          filter: search.filter,
          channel: search.channel,
        },
      }),
    [navigate, search.channel, search.filter, search.scope, slug],
  )

  const goBusinessSettings = useCallback(() => {
    void navigate({ to: "/t/$slug/settings/business-channels", params: { slug } })
  }, [navigate, slug])

  return (
    <Box style={{ display: "flex", height: "100%" }}>
      <ChatNav scope={scope} filter={filter} channelId={channelId} onSelect={selectNav} />
      {(!narrow || !id) && (
        <Pane
          width={narrow ? "100%" : undefined}
          border={narrow ? "none" : "right"}
          resizable={!narrow && { storageKey: "chat-list", defaultWidth: 320, min: 260, max: 420, side: "right" }}
        >
          <ChatList
            scope={scope}
            mode={filter}
            channelId={channelId}
            channelName={channelName}
            openId={id}
            open={open}
            onNewChat={() => setNewOpen(true)}
            onManageBusinessChannels={canUseBusiness ? goBusinessSettings : undefined}
          />
        </Pane>
      )}
      {(!narrow || id) && (
        <Box style={{ flex: 1, minWidth: 0, display: "flex" }}>
          <Outlet />
        </Box>
      )}
      <NewChatModal opened={newOpen} onClose={() => setNewOpen(false)} onOpened={(chat) => open(chat.id)} />
    </Box>
  )
}
