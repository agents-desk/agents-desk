import type { ContactKind, ContactSort } from "@agents-desk/client/tenant"

/** ContactsSearch 是联系人列表页的地址参数，支持按视图、类型、关键词、语言、渠道、标签筛选与排序。 */
export interface ContactsSearch {
  view?: string
  kind?: ContactKind
  q?: string
  language?: string
  channel?: string
  tag?: string
  sort: ContactSort
}

/** validateContactsSearch 解析并规范化联系人列表页的地址参数，无效的取值回退到默认值。 */
export function validateContactsSearch(raw: Record<string, unknown>): ContactsSearch {
  const view = typeof raw.view === "string" && raw.view.trim() ? raw.view.trim() : undefined
  const kind = raw.kind === "user" || raw.kind === "lead" || raw.kind === "internal" ? raw.kind : undefined
  const q = typeof raw.q === "string" && raw.q.trim() ? raw.q : undefined
  const language = typeof raw.language === "string" && raw.language.trim() ? raw.language : undefined
  const channel = typeof raw.channel === "string" && raw.channel.trim() ? raw.channel : undefined
  const tag = typeof raw.tag === "string" && raw.tag.trim() ? raw.tag : undefined
  const sort: ContactSort = raw.sort === "created" ? "created" : "recent"
  return { view, kind, q, language, channel, tag, sort }
}
