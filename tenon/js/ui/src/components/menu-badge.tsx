// 菜单项的徽标：在导航或菜单项旁显示数字、文字或红点，支持醒目未读（danger）与中性计数（muted）两种外观。

import { Box, type MantineColor, Text } from "@mantine/core"
import type { ReactNode } from "react"
import type { MenuItem } from "../app/menu"

/** MenuBadge 的选项。 */
export interface MenuBadgeProps {
  value: MenuItem["badge"]
  /** 外观变体：danger（默认，红底实心或红点，用于未读提醒）或 muted（中性等宽计数，用于列表总数）。 */
  variant?: "danger" | "muted"
  /** muted 变体下的高亮颜色（例如等待超时提醒）；省略时使用 dimmed 中性色。 */
  color?: MantineColor
  /** 数字超出该上限时显示 "<max>+"，仅对数字生效。 */
  max?: number
}

/** 显示菜单或导航项的徽标；value 为 undefined 或 false 时不渲染。 */
export function MenuBadge({ value, variant = "danger", color, max }: MenuBadgeProps): ReactNode {
  if (value === undefined || value === false) return null
  if (value === true) return <Box w={6} h={6} bg="red.6" style={{ borderRadius: "50%" }} />
  const display = typeof value === "number" && max !== undefined && value > max ? `${max}+` : value
  if (variant === "muted") {
    return (
      <Text
        component="span"
        size="xs"
        c={color ?? "dimmed"}
        fw={color ? 600 : undefined}
        style={{ fontVariantNumeric: "tabular-nums" }}
      >
        {display}
      </Text>
    )
  }
  return (
    <Text
      component="span"
      fz={10}
      fw={600}
      c="white"
      bg="red.6"
      px={5}
      lh="16px"
      style={{ borderRadius: 8, fontVariantNumeric: "tabular-nums" }}
    >
      {display}
    </Text>
  )
}
