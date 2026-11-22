import { Center, Stack, Text, ThemeIcon } from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { createFileRoute } from "@tanstack/react-router"
import { IconMessages } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"

// 还没有打开会话时，会话一栏的提示。
export const Route = createFileRoute("/t/$slug/inbox/")({ component: NoConversation })

function NoConversation() {
  const t = useT()
  const os = useOs()
  const key = os === "macos" || os === "ios" ? "⌘K" : "Ctrl+K"
  return (
    <Center style={{ flex: 1, background: "var(--tn-canvas)" }}>
      <Stack gap="xs" align="center" px="md">
        <ThemeIcon size={48} radius="xl" variant="light" color="gray">
          <IconMessages size={24} stroke={1.5} />
        </ThemeIcon>
        <Text fw={500} size="sm">
          {t("inbox.pick")}
        </Text>
        <Text c="dimmed" size="xs" ta="center">
          {t("inbox.pick_shortcut", { key })}
        </Text>
      </Stack>
    </Center>
  )
}
