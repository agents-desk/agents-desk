import { createUiTheme } from "@go-tenon/ui"
import { createContext, useContext } from "react"

/** DEFAULT_WIDGET_COLOR 是聊天窗口的默认主题色。 */
export const DEFAULT_WIDGET_COLOR = "bright-blue"

/** buildWidgetTheme 按聊天插件配置的主题色（预设名或 #rrggbb）生成聊天窗口的主题。 */
export function buildWidgetTheme(color: string) {
  return createUiTheme({ preset: "comfortable", primary: color || DEFAULT_WIDGET_COLOR })
}

/** WidgetBrandContext 提供更新根主题品牌色的方法，让聊天窗口动态应用渠道配置的主题色。 */
export const WidgetBrandContext = createContext<(color: string) => void>(() => {})

/** useSetWidgetBrand 返回更新聊天窗口品牌色的函数。 */
export function useSetWidgetBrand(): (color: string) => void {
  return useContext(WidgetBrandContext)
}
