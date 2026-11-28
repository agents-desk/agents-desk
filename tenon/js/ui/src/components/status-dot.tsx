// 状态点：带浅色光圈的圆点和可选文字，例如描述列表、卡片标题或菜单项中的状态标识。

import { Box, Group, type MantineColor, Text } from "@mantine/core"
import type { ReactNode } from "react"

/** StatusDot 的选项。 */
export interface StatusDotProps {
  /** 颜色：正常用 green，需要注意用 deep-orange，出错用 red，没有状态用 gray。 */
  color: MantineColor
  /** 仅渲染圆点时用于无障碍读屏的说明文字。 */
  label?: string
  /** 可选状态文字；省略时只渲染状态圆点。 */
  children?: ReactNode
}

/** 状态标识：带可选文字说明的状态圆点。 */
export function StatusDot({ color, label, children }: StatusDotProps): ReactNode {
  const dot = (
    <Box
      component="span"
      role={!children && label ? "img" : undefined}
      aria-label={!children ? label : undefined}
      aria-hidden={!children && label ? undefined : true}
      className="tn-status-dot-mark"
      style={{
        background: `var(--mantine-color-${color}-filled)`,
        boxShadow: `0 0 0 3px var(--mantine-color-${color}-light)`,
      }}
    />
  )
  if (!children) return dot
  return (
    <Group gap={6} wrap="nowrap" className="tn-status-dot" style={{ flexShrink: 0 }}>
      {dot}
      <Text span size="sm" fw={500} c={`var(--mantine-color-${color}-text)`}>
        {children}
      </Text>
    </Group>
  )
}
