import type { ConversationContactConversationDTO, ConversationContactItemDTO } from "@agents-desk/client/widget"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { authed, widgetApi } from "../api"
import { useIdentity } from "../realtime"

// 聊天窗口的查询。键都以 ["widget", 联系人] 开头：实时推送让 ["widget"] 整组失效；换了身份（登录、退出）时键随之变化，
// 不会看到上一个人的数据。

/** useConfig 读取聊天插件的公开设置：名称、问候语、主题色。 */
export function useConfig() {
  return useQuery({ queryKey: ["widget", "config"], queryFn: () => widgetApi.widget.config(), staleTime: 5 * 60_000 })
}

/** useConversations 读取客户的会话列表，最近有消息的在前。 */
export function useConversations() {
  const { contact } = useIdentity()
  return useQuery({
    queryKey: ["widget", contact?.id, "conversations"],
    queryFn: () => authed(() => widgetApi.conversation.list()),
    enabled: !!contact,
  })
}

/** useConversation 读取会话 id：状态和评价；id 为空（新会话）时不读取。 */
export function useConversation(id: string) {
  const { contact } = useIdentity()
  return useQuery<ConversationContactConversationDTO>({
    queryKey: ["widget", contact?.id, "conversation", id],
    queryFn: () => authed(() => widgetApi.conversation.get(id)),
    enabled: !!contact && !!id,
  })
}

/**
 * useItems 读取会话 id 的时间线，id 为空（新会话）时不读取。第一次读取全部；之后（推送、重连）只读取序号大于已有最后一项的，接在后面。
 */
export function useItems(id: string) {
  const { contact } = useIdentity()
  const queryClient = useQueryClient()
  const key = ["widget", contact?.id, "items", id]
  return useQuery<ConversationContactItemDTO[]>({
    queryKey: key,
    queryFn: async () => {
      const before = queryClient.getQueryData<ConversationContactItemDTO[]>(key) ?? []
      const after = before.at(-1)?.seq ?? 0
      const next = await authed(() => widgetApi.conversation.items(id, { after, limit: 500 }))
      return next.length === 0 ? before : [...before, ...next]
    },
    enabled: !!contact && !!id,
  })
}
