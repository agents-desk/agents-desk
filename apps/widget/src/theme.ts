import { createUiTheme, primaryColors } from "@go-tenon/ui/theme"
import { createContext, useContext } from "react"

/** DEFAULT_WIDGET_COLOR 是聊天窗口的默认主题色（#0063ff）。 */
export const DEFAULT_WIDGET_COLOR: string = primaryColors["bright-blue"]

/** isValidHexColor 报告 color 是否为 #rrggbb 十六进制颜色值。 */
export function isValidHexColor(color: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(color)
}

/** buildWidgetTheme 按聊天插件配置的主题色生成聊天窗口的主题。 */
export function buildWidgetTheme(color: string) {
  const primary = isValidHexColor(color) ? color : DEFAULT_WIDGET_COLOR
  return createUiTheme({ preset: "comfortable", primary })
}

/** WidgetBrandContext 提供更新根主题品牌色的方法，让聊天窗口动态应用渠道配置的主题色。 */
export const WidgetBrandContext = createContext<(color: string) => void>(() => {})

/** useSetWidgetBrand 返回更新聊天窗口品牌色的函数。 */
export function useSetWidgetBrand(): (color: string) => void {
  return useContext(WidgetBrandContext)
}
