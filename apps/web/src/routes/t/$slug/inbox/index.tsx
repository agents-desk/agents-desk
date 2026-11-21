import { Center, Stack, Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { IconMessages } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"

// 还没有打开会话时，会话一栏的提示。
export const Route = createFileRoute("/t/$slug/inbox/")({ component: NoConversation })

function NoConversation() {
  const t = useT()
  return (
    <Center style={{ flex: 1 }}>
      <Stack gap={6} align="center">
        <IconMessages size={36} color="var(--mantine-color-gray-5)" />
        <Text c="dimmed" size="sm">
          {t("inbox.pick")}
        </Text>
      </Stack>
    </Center>
  )
}
