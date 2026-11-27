import { useEffect } from "react"

/** NARROW_QUERY 是工作台端统一的窄屏断点（小于 900px）：二级导航收进抽屉，列表与详情轮流占满。 */
export const NARROW_QUERY = "(max-width: 899px)"

/**
 * isTypingTarget 报告键盘事件是否落在输入框、下拉选择、富文本或弹窗中：
 * 为真时工作台单键快捷键（如 J/K/I/R/E）暂停，按键交给输入框。
 */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true
  return Boolean(target.closest("[role='dialog'], [role='menu'], [role='listbox'], [contenteditable='true']"))
}

/**
 * useListKeys 让列表支持非输入态按 J/K 打开下一条/上一条（聊天、工单列表用；收件箱另外处理搜索结果）。
 * ids 是列表当前的顺序，openId 是打开着的一条，没打开时 J/K 都打开第一条。
 * warm 预取一条的数据：打开一条后预取它的上一条和下一条，按 J/K 时直接显示。
 */
export function useListKeys(ids: string[], openId: string | undefined, open: (id: string) => void, warm?: (id: string) => void): void {
  useEffect(() => {
    if (!warm || !openId) return
    const at = ids.indexOf(openId)
    if (at < 0) return
    for (const id of [ids[at + 1], ids[at - 1]]) if (id) warm(id)
  }, [ids, openId, warm])
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return
      const key = e.key.toLowerCase()
      if ((key !== "j" && key !== "k") || ids.length === 0) return
      e.preventDefault()
      const at = openId ? ids.indexOf(openId) : -1
      const next = key === "j" ? (at < 0 ? 0 : Math.min(ids.length - 1, at + 1)) : at <= 0 ? 0 : at - 1
      if (ids[next] !== openId) open(ids[next])
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [ids, openId, open])
}
