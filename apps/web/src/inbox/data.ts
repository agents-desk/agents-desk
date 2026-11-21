import type { ConversationDTO, ConversationItemDTO, MemberTenantMember } from "@agents-desk/client/tenant"
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query"
import { kit, tenantApi } from "../api"
import { me } from "../auth"
import type { InboxSearch } from "./search"

// 收件箱的查询。键都在 kit.queries.conversation.key() 之下：实时推送让这一组失效，列表、会话和时间线随之重新读取。

/** useMe 返回当前登录的成员。 */
export const useMe = () => useQuery({ queryKey: ["me"], queryFn: me })

/**
 * useMembers 返回工作区的成员，按 ID 查名字和头像（时间线、负责人、@ 同事、指派）。一次读取最多 100 位，
 * 第一版的工作区不超过这个人数。
 */
export function useMembers(): { list: MemberTenantMember[]; byId: Map<string, MemberTenantMember> } {
  const q = useQuery({ queryKey: ["members", "all"], queryFn: () => tenantApi.member.list({ per_page: 100 }), staleTime: 60_000 })
  const list = q.data?.items ?? []
  return { list, byId: new Map(list.map((m) => [m.id, m])) }
}

/** useTeams 返回工作区的团队收件箱：指派和详情中选择。 */
export function useTeams() {
  const q = useQuery({ queryKey: ["teams", "all"], queryFn: () => tenantApi.team.list({ per_page: 100 }), staleTime: 60_000 })
  return q.data?.items ?? []
}

/** Thresholds 是等待时长变色的时限（秒）：超过 warn 是“等待处理”，超过 alert 是“需要注意”。 */
export interface Thresholds {
  warn: number
  alert: number
}

/** useThresholds 读取工作区的回复时间换算出的时限；还没读到时是 undefined，等待时长不变色。 */
export function useThresholds(): Thresholds | undefined {
  const q = useQuery({ queryKey: ["business-hours"], queryFn: () => tenantApi.hours.get(), staleTime: 5 * 60_000 })
  return q.data ? { warn: q.data.warn_after, alert: q.data.alert_after } : undefined
}

/** listKey 是会话列表的查询键。 */
const listKey = (s: InboxSearch) => [...kit.queries.conversation.key(), "inbox-list", s.box, s.team ?? "", s.status, s.sort]

/** useConversationList 读取当前列表的会话，游标分页，滚到底时加载下一页。 */
export function useConversationList(s: InboxSearch) {
  return useInfiniteQuery({
    queryKey: listKey(s),
    queryFn: ({ pageParam }) =>
      tenantApi.conversation.list({
        inbox: s.box,
        team_id: s.team,
        status: s.status,
        sort: s.sort,
        cursor: pageParam || undefined,
        per_page: 50,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor,
  })
}

/** useConversation 读取会话 id。 */
export const useConversation = (id: string) => useQuery(kit.queries.conversation.get(id))

/**
 * useTimeline 读取会话 id 的时间线。第一次读取全部（最多 500 项）；之后推送让它失效时，从已有最后一项往前 50 个序号开始读：
 * 这一段中已有的项换成最新的（送达状态会变成已送达、已读、失败），新的项接在后面。
 */
export function useTimeline(id: string) {
  const queryClient = useQueryClient()
  const key = [...kit.queries.conversation.key(), "timeline", id]
  return useQuery<ConversationItemDTO[]>({
    queryKey: key,
    queryFn: async () => {
      const before = queryClient.getQueryData<ConversationItemDTO[]>(key) ?? []
      const last = before.at(-1)?.seq ?? 0
      const recent = await tenantApi.conversation.items(id, { after: Math.max(0, last - 50), limit: 500 })
      const latest = new Map(recent.map((it) => [it.id, it]))
      return [...before.map((it) => latest.get(it.id) ?? it), ...recent.filter((it) => it.seq > last)]
    },
  })
}

/** contactHistory 读取联系人的会话（详情中的“最近的会话”），包括已关闭的。 */
export function useContactHistory(contactId: string | undefined) {
  return useQuery({
    queryKey: [...kit.queries.conversation.key(), "contact-history", contactId],
    queryFn: () => tenantApi.conversation.list({ contact_id: contactId, per_page: 10, sort: "recent" }),
    enabled: !!contactId,
  })
}

/** flatten 把分页的会话列表展开成一个数组。 */
export const flatten = (pages: { items: ConversationDTO[] }[] | undefined) => (pages ?? []).flatMap((p) => p.items)
