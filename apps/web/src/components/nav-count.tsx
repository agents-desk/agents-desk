import { Badge, type MantineColor, Text } from "@mantine/core"
import type { ReactNode } from "react"

/**
 * NavCount 是二级导航项右边的数字，全工作台只有两种样式：
 * - 数量（默认）：灰色数字，例如收件箱中的会话数、联系人数；color 用于标出需要注意的数量（等待超时）。
 * - 未读（unread）：品牌色的实心徽章，只用于未读消息，超过 99 显示 99+，为 0 时不显示。
 */
export function NavCount({ n, unread, color }: { n?: number; unread?: boolean; color?: MantineColor }): ReactNode {
  if (n === undefined) return null
  if (unread) {
    if (n <= 0) return null
    return (
      <Badge size="xs" circle={n < 10} color="brand" style={{ fontVariantNumeric: "tabular-nums" }}>
        {n > 99 ? "99+" : n}
      </Badge>
    )
  }
  return (
    <Text size="xs" c={color ?? "dimmed"} fw={color ? 600 : undefined} style={{ fontVariantNumeric: "tabular-nums" }}>
      {n}
    </Text>
  )
}
