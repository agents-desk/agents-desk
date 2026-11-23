/** NARROW_QUERY 是工作台端统一的窄屏断点（小于 900px）：二级导航收进抽屉，列表与详情轮流占满。 */
export const NARROW_QUERY = "(max-width: 899px)"

/**
 * isTypingTarget 报告键盘事件是否落在输入框、下拉选择、富文本或弹窗中：
 * 为真时工作台单键快捷键（如 J/K/I/R/E）不触发，避免打断正常输入。
 */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true
  return Boolean(target.closest("[role='dialog'], [role='menu'], [role='listbox'], [contenteditable='true']"))
}
