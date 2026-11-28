// 表单分组卡片：带边框的卡片容器，顶部展示标题、可选说明与右侧状态/操作。

import { Box, Group, Paper, Stack, Text, Title } from "@mantine/core"
import type { ReactNode } from "react"

/** FormSection 的选项。 */
export interface FormSectionProps {
  title: ReactNode
  description?: ReactNode
  /** 标题右侧的内容，例如连接状态或操作按钮。 */
  actions?: ReactNode
  children: ReactNode
}

/** 设置页表单中的一组输入项卡片。 */
export function FormSection({ title, description, actions, children }: FormSectionProps): ReactNode {
  return (
    <Paper withBorder p="md">
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
