import { Center, Stack, Text, ThemeIcon } from "@mantine/core"
import type { ReactNode } from "react"

/** EmptyStateProps 是空状态的选项。 */
export interface EmptyStateProps {
  /** 标题上面的图标；列表栏中的空状态通常不带图标。 */
  icon?: ReactNode
  /** 主要的一句话，例如“这个列表中没有会话”。 */
  title: ReactNode
  /** 标题下面的补充说明。 */
  description?: ReactNode
  /** 下面的操作，例如“清除筛选”“新建”按钮，按钮用 size="xs" variant="light"。 */
  action?: ReactNode
  /** 占满所在区域并垂直居中，用于详情栏的“从左边选一个”。 */
  fill?: boolean
}

/**
 * EmptyState 是工作台统一的空状态：可选的图标、一句标题、补充说明和操作按钮，水平居中。
 * 列表栏、设置页和详情栏都用它，文字大小、间距和按钮尺寸保持一致。
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
