import { confirm, useT } from "@go-tenon/ui"
import { useBlocker } from "@tanstack/react-router"

/**
 * useUnsavedGuard 在表单有未保存的修改时拦住离开：站内跳到别的页面前弹出确认，关闭或刷新浏览器标签时由浏览器提示。
 * 只拦地址路径的变化，同一页面内切换标签（?tab=）不拦。
 */
export function useUnsavedGuard(dirty: boolean): void {
  const t = useT()
  useBlocker({
    shouldBlockFn: async ({ current, next }) => {
      if (!dirty || current.pathname === next.pathname) return false
      const leave = await confirm({
        title: t("common.unsaved_title"),
        message: t("common.unsaved_message"),
        confirmLabel: t("common.leave"),
        danger: true,
      })
      return !leave
    },
    enableBeforeUnload: dirty,
  })
}
