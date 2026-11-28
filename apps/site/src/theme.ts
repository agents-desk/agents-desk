import { createUiTheme } from "@go-tenon/ui/core"

const fontFamily =
  '"Inter Variable", Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "PingFang SC", "Hiragino Sans GB", "Noto Sans SC", "Noto Sans CJK SC", "Microsoft YaHei", sans-serif'

// 官网采用宽松预设（spacious）与工作台统一的品牌色（dark-pink）。
export const theme = createUiTheme({
  preset: "spacious",
  primary: "dark-pink",
  fontFamily,
})
