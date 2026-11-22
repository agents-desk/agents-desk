/** LIGHT_PRESETS 是明度偏高的主题色预设，在其填充底色上应使用深色文字以保证对比度。 */
const LIGHT_PRESETS = new Set(["yellow", "bright-orange", "bright-green", "green", "light-blue"])

/** onBrandColor 返回在主题色 color 填充背景上的可读前景色（深灰或纯白）。 */
export function onBrandColor(color: string): "dark.9" | "white" {
  return LIGHT_PRESETS.has(color) ? "dark.9" : "white"
}
