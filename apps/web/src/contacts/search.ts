import type { ContactKind, ContactSort } from "@agents-desk/client/tenant"

/** ContactsSearch 是联系人列表页的地址参数，按类型、关键词、语言、渠道筛选，带排序；分页走游标滚动加载。 */
export interface ContactsSearch {
  kind?: ContactKind
  q?: string
  language?: string
  channel?: string
  sort: ContactSort
}

/** validateContactsSearch 解析并规范化联系人列表页的地址参数，无效的取值回退到默认值。 */
export function validateContactsSearch(raw: Record<string, unknown>): ContactsSearch {
  const kind = raw.kind === "user" || raw.kind === "lead" ? raw.kind : undefined
  const q = typeof raw.q === "string" && raw.q.trim() ? raw.q : undefined
  const language = typeof raw.language === "string" && raw.language.trim() ? raw.language : undefined
  const channel = typeof raw.channel === "string" && raw.channel.trim() ? raw.channel : undefined
  const sort: ContactSort = raw.sort === "created" ? "created" : "recent"
  return { kind, q, language, channel, sort }
}
