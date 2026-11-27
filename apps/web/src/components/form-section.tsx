import { Box, Group, Paper, Stack, Text, Title } from "@mantine/core"
import type { ReactNode } from "react"

/**
 * FormSection 是设置页表单中的一组输入项：带边框的卡片，顶部是标题和一行说明，右上角可放状态或按钮。
 * 渠道配置页、商务渠道的策略和 Telegram 接入都用它分组。
 */
export function FormSection({ title, description, actions, children }: {
  title: ReactNode
  description?: ReactNode
  /** 标题右侧的内容，例如连接状态、重连按钮。 */
  actions?: ReactNode
  children: ReactNode
}): ReactNode {
  return (
    <Paper withBorder p="md" radius="md">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start" gap="sm" wrap="wrap">
          <Box style={{ flex: 1, minWidth: 200 }}>
            <Title order={5}>{title}</Title>
            {description && (
              <Text size="xs" c="dimmed" mt={2}>
                {description}
              </Text>
            )}
          </Box>
          {actions}
        </Group>
        {children}
      </Stack>
    </Paper>
  )
}
