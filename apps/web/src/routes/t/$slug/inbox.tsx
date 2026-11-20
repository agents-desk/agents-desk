import { Center, Stack, Text, Title } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"

// 收件箱：坐席接待会话的地方。铺满内容区（fullBleed），二级导航是带数量的收件箱列表（我的、@我、未指派、全部、
// 各团队收件箱、视图），右侧是会话列表和会话，见产品设计的 13.2。现在是占位页，列表和会话在 D1、D2 中完成。
export const Route = createFileRoute("/t/$slug/inbox")({
  staticData: { title: "收件箱", icon: "inbox", order: 1, permission: "conversations:read", fullBleed: true },
  component: Inbox,
})

function Inbox() {
  return (
    <Center h="100%">
      <Stack gap={4} align="center">
        <Title order={4}>收件箱</Title>
        <Text c="dimmed" size="sm">
          会话列表和会话在收件箱的开发（D1、D2）中加入。
        </Text>
      </Stack>
    </Center>
  )
}
