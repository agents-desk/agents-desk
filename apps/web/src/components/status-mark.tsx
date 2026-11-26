import type { MantineColor } from "@mantine/core"
import type { ReactNode } from "react"

/**
 * StatusMark 是不带文字的状态圆点（8px），用于导航项、菜单项和列表行中只需一个色点的地方；
 * 需要同时显示文字时用 @go-tenon/ui 的 StatusDot。color 是主题中的色板名，例如 green、red、gray。
 */
export function StatusMark({ color, label }: { color: MantineColor; label?: string }): ReactNode {
  return (
    <span
      className="desk-status-mark"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      style={{ background: `var(--mantine-color-${color}-filled)` }}
    />
  )
}
