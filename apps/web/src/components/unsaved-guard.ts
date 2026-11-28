import { confirm, type UnsavedChanges, useT, useUnsavedChanges } from "@go-tenon/ui"

/**
 * useUnsavedGuard 复用 @go-tenon/ui 的 useUnsavedChanges 并接入工作台统一的放弃修改确认弹窗；
 * 仅拦截路由路径变化（切换页面 ?tab= 不触发）。
 */
export function useUnsavedGuard(dirty: boolean): UnsavedChanges {
  const t = useT()
  return useUnsavedChanges(dirty, {
    ignoreSearch: true,
    confirm: () =>
      confirm({
        title: t("common.unsaved_title"),
        message: t("common.unsaved_message"),
        confirmLabel: t("common.leave"),
        danger: true,
      }),
  })
}
