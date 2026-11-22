import { useSyncExternalStore } from "react"

/** DraftMode 是草稿保存时的回复框模式：回复或内部备注。 */
export type DraftMode = "reply" | "note"

/** ConversationDraft 是单条会话未发送的草稿：文本、模式以及备注中已选中的被 @ 成员编号。 */
export interface ConversationDraft {
  text: string // 输入框原文
  mode: DraftMode // 回复或内部备注
  mentionIds: string[] // 备注中已选中的被 @ 成员编号
}

/** STORAGE_KEY 是草稿在 sessionStorage 中的键名。 */
const STORAGE_KEY = "agents-desk:inbox-drafts"

/** MAX_DRAFTS 是最多保留的草稿条数，避免存储无限增长。 */
const MAX_DRAFTS = 50

const listeners = new Set<() => void>()
let drafts = load()

/** load 从 sessionStorage 读出已保存的草稿表，解析失败时返回空表。 */
function load(): Map<string, ConversationDraft> {
  if (typeof window === "undefined") return new Map()
  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return new Map()
    const entries = JSON.parse(raw) as [string, ConversationDraft][]
    if (!Array.isArray(entries)) return new Map()
    const map = new Map<string, ConversationDraft>()
    for (const [id, d] of entries) {
      if (typeof id === "string" && d && typeof d.text === "string" && (d.mode === "reply" || d.mode === "note")) {
        map.set(id, { text: d.text, mode: d.mode, mentionIds: Array.isArray(d.mentionIds) ? d.mentionIds : [] })
      }
    }
    return map
  } catch {
    return new Map()
  }
}

/** persist 把当前草稿表写回 sessionStorage 并通知订阅者。 */
function persist(): void {
  try {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify([...drafts.entries()]))
  } catch {
    // 浏览器禁用或写满存储时仅在当前内存中保留
  }
  for (const fn of listeners) fn()
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** getDraft 返回会话 id 当前保存的草稿，没有时返回 undefined。 */
export function getDraft(id: string): ConversationDraft | undefined {
  return drafts.get(id)
}

/** saveDraft 保存会话 id 的草稿；文本全为空白时直接移除该条草稿。 */
export function saveDraft(id: string, draft: ConversationDraft): void {
  if (!draft.text.trim()) {
    clearDraft(id)
    return
  }
  const next = new Map(drafts)
  // 刷新顺序：最近编辑的排在末尾，超出上限时淘汰最早的草稿
  next.delete(id)
  next.set(id, draft)
  while (next.size > MAX_DRAFTS) {
    const oldest = next.keys().next().value
    if (oldest === undefined) break
    next.delete(oldest)
  }
  drafts = next
  persist()
}

/** clearDraft 清除会话 id 的草稿（例如发送成功或清空输入后）。 */
export function clearDraft(id: string): void {
  if (!drafts.has(id)) return
  const next = new Map(drafts)
  next.delete(id)
  drafts = next
  persist()
}

/** useDraft 订阅会话 id 的草稿，变化时触发重新渲染。 */
export function useDraft(id: string): ConversationDraft | undefined {
  return useSyncExternalStore(
    subscribe,
    () => drafts.get(id),
    () => undefined,
  )
}
