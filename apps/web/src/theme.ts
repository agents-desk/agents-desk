import { createUiTheme } from "@go-tenon/ui"

// 工作台字体：西文和数字优先使用随包引入的 Inter Variable，中文与全角标点由系统字体渲染。
const fontFamily =
  '"Inter Variable", Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "PingFang SC", "Hiragino Sans GB", "Noto Sans SC", "Noto Sans CJK SC", "Microsoft YaHei", sans-serif'

// 主题：舒展预设（comfortable）配合品牌色与日期格式；工具条与检查器通过框架的 dense/inspector 密度作用域保持紧凑高度与清晰字号。
export const theme = createUiTheme({
  preset: "comfortable",
  primary: "dark-pink",
  fontFamily,
  datetimeFormat: "MM/DD HH:mm",
})
