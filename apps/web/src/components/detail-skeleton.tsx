import { Box, Group, Skeleton, Stack } from "@mantine/core"
import { PaneHeader } from "@go-tenon/ui"
import type { ReactNode } from "react"

// 打开会话、聊天、工单时，数据还没到（直接打开链接或网络较慢）就先显示与真实页面同样布局的骨架：
// 顶栏、时间线、输入框都在原位，数据到达后只替换内容。

/** ConversationSkeleton 是会话、聊天、工单主栏的骨架：52px 顶栏、消息气泡和底部输入框。 */
export function ConversationSkeleton({ back, composer = true }: { back?: () => void; composer?: boolean }): ReactNode {
  return (
    <Box style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }} aria-busy>
      <PaneHeader fixed back={back}>
        <Stack gap={6} style={{ flex: 1, minWidth: 0 }}>
          <Skeleton h={14} w={180} radius="sm" />
          <Skeleton h={10} w={260} radius="sm" />
        </Stack>
      </PaneHeader>
      <Box className="inbox-timeline" px="var(--tn-pane-px)" py="md">
        <TimelineSkeleton />
      </Box>
      {composer && (
        <Box className="inbox-composer">
          <Box className="inbox-composer-box" h={150} />
        </Box>
      )}
    </Box>
  )
}

/** TimelineSkeleton 是时间线中带左右双侧头像占位的交错消息气泡骨架。 */
export function TimelineSkeleton(): ReactNode {
  return (
    <Stack gap="md">
      <Box style={{ alignSelf: "flex-start", width: "58%", maxWidth: 400 }}>
        <Skeleton h={10} w={88} ml={42} mb={6} radius="sm" />
        <Group gap={10} wrap="nowrap" align="flex-start">
          <Skeleton circle h={32} w={32} mt={3} style={{ flex: "none" }} />
          <Skeleton h={52} radius="md" style={{ flex: 1 }} />
        </Group>
      </Box>
      <Box style={{ alignSelf: "flex-end", width: "50%", maxWidth: 360 }}>
        <Skeleton h={10} w={72} ml="auto" mr={42} mb={6} radius="sm" />
        <Group gap={10} wrap="nowrap" align="flex-start" style={{ flexDirection: "row-reverse" }}>
          <Skeleton circle h={32} w={32} mt={3} style={{ flex: "none" }} />
          <Skeleton h={42} radius="md" style={{ flex: 1 }} />
        </Group>
      </Box>
      <Box style={{ alignSelf: "flex-start", width: "64%", maxWidth: 440 }}>
        <Skeleton h={10} w={96} ml={42} mb={6} radius="sm" />
        <Group gap={10} wrap="nowrap" align="flex-start">
          <Skeleton circle h={32} w={32} mt={3} style={{ flex: "none" }} />
          <Skeleton h={64} radius="md" style={{ flex: 1 }} />
        </Group>
      </Box>
    </Stack>
  )
}
