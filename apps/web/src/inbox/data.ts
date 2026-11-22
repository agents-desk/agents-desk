import type { AttributeTarget, ConversationDTO, ConversationItemDTO, ConversationSearchHitDTO, MemberTenantMember } from "@agents-desk/client/tenant"
import { fetchAllPages } from "@go-tenon/client"
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query"
import { kit, tenantApi } from "../api"
import { me } from "../auth"
import { consumeRefreshFrom } from "../realtime"
import type { InboxSearch } from "./search"

// 收件箱的查询。键都在 kit.queries.conversation.key() 之下：实时推送让这一组失效，列表、会话和时间线随之重新读取。

/** useMe 返回当前登录的成员。 */
export const useMe = () => useQuery({ queryKey: ["me"], queryFn: me })

/** useMembers 返回工作区的全部成员，按 ID 查名字和头像（时间线、负责人、@ 同事、指派）。 */
export function useMembers(): { list: MemberTenantMember[]; byId: Map<string, MemberTenantMember> } {
  const q = useQuery({ queryKey: [...kit.queries.member.key(), "all"], queryFn: () => fetchAllPages((p) => tenantApi.member.list(p)), staleTime: 60_000 })
  const list = q.data ?? []
  return { list, byId: new Map(list.map((m) => [m.id, m])) }
}

/** useTeams 返回工作区的全部团队收件箱：指派和详情中选择。 */
export function useTeams() {
  const q = useQuery({ queryKey: [...kit.queries.team.key(), "all"], queryFn: () => fetchAllPages((p) => tenantApi.team.list(p)), staleTime: 60_000 })
  return q.data ?? []
}

/** useTags 返回工作区的全部标签：收件箱筛选和详情中选择。 */
export function useTags() {
  const q = useQuery({ queryKey: [...kit.queries.tag.key(), "all"], queryFn: () => fetchAllPages((p) => tenantApi.tag.list(p)), staleTime: 60_000 })
  return q.data ?? []
}

/** useAttributes 返回工作区指定对象的自定义属性定义，按展示顺序排列。 */
export function useAttributes(target: AttributeTarget) {
  const q = useQuery({ ...kit.queries.attribute.list({ target }), staleTime: 60_000 })
  return q.data ?? []
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
const listKey = (s: InboxSearch) => [...kit.queries.conversation.key(), "inbox-list", s.box, s.team ?? "", s.tag ?? "", s.status, s.sort]

/** useConversationList 读取当前列表的会话，游标分页，滚到底时加载下一页。 */
export function useConversationList(s: InboxSearch) {
  return useInfiniteQuery({
    queryKey: listKey(s),
    queryFn: ({ pageParam }) =>
      tenantApi.conversation.list({
        inbox: s.box,
        team_id: s.team,
        tag_id: s.tag,
        status: s.status,
        sort: s.sort,
        cursor: pageParam || undefined,
        per_page: 50,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor,
    enabled: !s.q?.trim(),
  })
}

/** useConversationSearch 按关键词全文搜索会话，支持全部可见会话与当前列表两种范围，滚到底时加载下一页。 */
export function useConversationSearch(s: InboxSearch) {
  const q = s.q?.trim() ?? ""
  const scope = s.scope ?? "all"
  return useInfiniteQuery({
    queryKey: [
      ...kit.queries.conversation.key(),
      "search",
      q,
      scope,
      scope === "current" ? s.box : "",
      scope === "current" ? (s.team ?? "") : "",
      s.tag ?? "",
    ],
    queryFn: ({ pageParam }) =>
      tenantApi.conversation.search({
        q,
        inbox: scope === "current" ? s.box : undefined,
        team_id: scope === "current" ? s.team : undefined,
        tag_id: s.tag,
        page: pageParam,
        per_page: 20,
      }),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.page * last.per_page < last.total ? last.page + 1 : undefined),
    enabled: q.length > 0,
  })
}

/** flattenSearchHits 把分页的搜索结果展开成一个数组。 */
export const flattenSearchHits = (pages: { items: ConversationSearchHitDTO[] }[] | undefined) => (pages ?? []).flatMap((p) => p.items)

/** useConversation 读取会话 id。 */
export const useConversation = (id: string) => useQuery(kit.queries.conversation.get(id))

/**
 * useTimeline 读取会话 id 的时间线。第一次读取全部（最多 500 项）；之后推送让它失效时，从已有最后一项往前 50 个序号开始读：
 * 这一段中已有的项换成最新的（送达状态会变成已送达、已读、失败），新的项接在后面。若推送指出有更早的历史消息更新了派生内容
 * （例如看到历史消息时触发了补译），或者需要定位到搜索命中的较早消息 focusSeq，自动拉取对应序号附近窗口并按 seq 合并。
 */
export function useTimeline(id: string, focusSeq?: number) {
  const queryClient = useQueryClient()
  const key = [...kit.queries.conversation.key(), "timeline", id]
  return useQuery<ConversationItemDTO[]>({
    queryKey: [...key, focusSeq ?? 0],
    queryFn: async () => {
      const before = queryClient.getQueryData<ConversationItemDTO[]>(key) ?? []
      const last = before.at(-1)?.seq ?? 0
      const tailAfter = Math.max(0, last - 50)
      const from = consumeRefreshFrom(id)
      const needFocus = focusSeq !== undefined && focusSeq > 0 && !before.some((it) => it.seq === focusSeq)
      let updates: ConversationItemDTO[]
      if (needFocus) {
        const [around, recent] = await Promise.all([
          tenantApi.conversation.items(id, { around: focusSeq, limit: 200 }),
          tenantApi.conversation.items(id, { after: tailAfter, limit: 500 }),
        ])
        updates = [...around, ...recent]
      } else if (from !== undefined && before.length > 0 && from <= tailAfter) {
        const start = Math.max(0, from - 1)
        if (last - start <= 500) {
          updates = await tenantApi.conversation.items(id, { after: start, limit: 500 })
        } else {
          // 会话超过 500 条时分段拉：一段覆盖历史补译窗口，一段覆盖尾部新消息和送达状态
          const [older, recent] = await Promise.all([
            tenantApi.conversation.items(id, { after: start, limit: 100 }),
            tenantApi.conversation.items(id, { after: tailAfter, limit: 500 }),
          ])
          updates = [...older, ...recent]
        }
      } else {
        updates = await tenantApi.conversation.items(id, { after: tailAfter, limit: 500 })
      }
      const merged = new Map<string, ConversationItemDTO>()
      for (const it of before) merged.set(it.id, it)
      for (const it of updates) merged.set(it.id, it)
      const result = [...merged.values()].sort((a, b) => a.seq - b.seq)
      queryClient.setQueryData(key, result)
      return result
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
