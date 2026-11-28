// 键盘快捷键与输入目标识别辅助工具。

/** 报告事件目标是否处于文本输入、下拉选择、富文本或弹窗/菜单内部，用于屏蔽全局单键快捷键。 */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (typeof HTMLElement === "undefined" || !(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true
  return Boolean(target.closest("[role='dialog'], [role='menu'], [role='listbox'], [contenteditable='true']"))
}
