import { SegmentedControl } from "@mantine/core"
import { createUiTheme } from "@go-tenon/ui"

// 主题：样式预设（compact 紧凑、comfortable 舒展、spacious 宽松）加上品牌色。改品牌色只改这里。
// 品牌色写预设的名称（primaryColors，例如 sky-blue）或者颜色值；datetimeFormat 是日期时间的显示格式。
export const theme = createUiTheme({
  preset: "compact",
  primary: "dark-pink",
  datetimeFormat: "MM/DD HH:mm",
  override: {
    components: {
      SegmentedControl: SegmentedControl.extend({
        defaultProps: {
          radius: "md",
          withItemsBorders: false,
          transitionDuration: 180,
          transitionTimingFunction: "cubic-bezier(0.2, 0.8, 0.2, 1)",
        },
      }),
    },
  },
})
