// 报表内容区块卡片：统一的边框、标题、可选副标题/操作区、骨架屏加载态和空态提示。

import { Center, Group, Paper, Skeleton, Stack, Text, Title } from "@mantine/core"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"

export interface SectionCardProps {
  title: ReactNode
  subtitle?: ReactNode
  action?: ReactNode
  loading?: boolean
  empty?: boolean
  emptyText?: ReactNode
  minHeight?: number
  children: ReactNode
}

/** 报表的图表或表格分区卡片。 */
export function SectionCard({
  title,
  subtitle,
  action,
  loading,
  empty,
  emptyText,
  minHeight = 160,
  children,
}: SectionCardProps): ReactNode {
  const t = useT()
  return (
    <Paper withBorder radius="md" p="md">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start" gap="xs" wrap="wrap">
          <div>
            <Title order={4} fz="sm" fw={600}>
              {title}
            </Title>
            {subtitle && (
              <Text size="xs" c="dimmed" mt={2}>
                {subtitle}
              </Text>
            )}
          </div>
          {action}
        </Group>

        {loading ? (
          <Skeleton height={minHeight} radius="sm" />
        ) : empty ? (
          <Center mih={minHeight}>
            <Text size="sm" c="dimmed">
              {emptyText ?? t("reports.empty")}
            </Text>
          </Center>
        ) : (
          children
        )}
      </Stack>
    </Paper>
  )
}
