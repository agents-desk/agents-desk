// 统一空状态：可选图标、主标题、补充说明与操作按钮，支持占满父容器并垂直居中（fill）。

import { Center, Stack, Text, ThemeIcon } from "@mantine/core"
import type { ReactNode } from "react"

/** EmptyState 的选项。 */
export interface EmptyStateProps {
  /** 标题上方的图标；紧凑列表栏中的空状态通常不带图标。 */
  icon?: ReactNode
  /** 主要的一句话，例如“暂无会话”。 */
  title: ReactNode
  /** 标题下方的补充说明。 */
  description?: ReactNode
  /** 下方的操作按钮（建议使用 size="xs" variant="light"）。 */
  action?: ReactNode
  /** 占满所在区域并垂直居中，常用于右侧详情空白占位。 */
  fill?: boolean
}

/**
 * 统一空状态：可选图标、一句标题、补充说明和操作按钮，水平居中。
 * 列表栏、设置页和详情空白区共用，保持字体大小、间距和按钮尺寸一致。
 */
export function EmptyState({ icon, title, description, action, fill }: EmptyStateProps): ReactNode {
  const body = (
    <Stack gap="sm" align="center" px="md" py={fill ? undefined : "xl"} maw={420} mx="auto">
      {icon && (
        <ThemeIcon size={48} radius="xl" variant="light" color="gray">
          {icon}
        </ThemeIcon>
      )}
      <Stack gap={4} align="center">
        <Text size="sm" fw={icon ? 500 : undefined} c={icon ? undefined : "dimmed"} ta="center">
          {title}
        </Text>
        {description && (
          <Text size="xs" c="dimmed" ta="center">
            {description}
          </Text>
        )}
      </Stack>
      {action}
    </Stack>
  )
  if (!fill) return body
  return <Center style={{ flex: 1, background: "var(--tn-canvas)" }}>{body}</Center>
}
