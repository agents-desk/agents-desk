import { isLightColor, useMantineTheme } from "@mantine/core"

/** onBrandColor 按填充色值的亮度返回可读前景色（深灰或纯白），阈值与 Mantine 的 autoContrast 一致。 */
export function onBrandColor(filledHex: string): "dark.9" | "white" {
  return isLightColor(filledHex, 0.3) ? "dark.9" : "white"
}

/** useBrandForeground 返回当前主题品牌色填充背景上的可读前景色（深灰或纯白）。 */
export function useBrandForeground(): "dark.9" | "white" {
  const theme = useMantineTheme()
  return onBrandColor(theme.colors.brand[6])
}
