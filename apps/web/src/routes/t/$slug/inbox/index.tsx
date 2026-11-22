import { Center, Group, Kbd, Stack, Text, ThemeIcon } from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { createFileRoute } from "@tanstack/react-router"
import { IconMessages } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"

// 还没有打开会话时，会话一栏的提示与常用快捷键导览。
export const Route = createFileRoute("/t/$slug/inbox/")({ component: NoConversation })

function NoConversation() {
  const t = useT()
  const os = useOs()
  const key = os === "macos" || os === "ios" ? "⌘K" : "Ctrl+K"
  return (
    <Center style={{ flex: 1, background: "var(--tn-canvas)" }}>
      <Stack gap="sm" align="center" px="md">
        <ThemeIcon size={48} radius="xl" variant="light" color="gray">
          <IconMessages size={24} stroke={1.5} />
        </ThemeIcon>
        <Stack gap={4} align="center">
          <Text fw={500} size="sm">
            {t("inbox.pick")}
          </Text>
          <Text c="dimmed" size="xs" ta="center">
            {t("inbox.pick_shortcut", { key })}
          </Text>
        </Stack>
        <Group gap="md" justify="center" mt={4}>
          <Group gap={4}>
            <Kbd size="xs">J</Kbd>
            <Kbd size="xs">K</Kbd>
            <Text size="xs" c="dimmed">
              {t("inbox.shortcut_nav")}
            </Text>
          </Group>
          <Group gap={4}>
            <Kbd size="xs">R</Kbd>
            <Text size="xs" c="dimmed">
              {t("inbox.shortcut_reply")}
            </Text>
          </Group>
          <Group gap={4}>
            <Kbd size="xs">I</Kbd>
            <Text size="xs" c="dimmed">
              {t("inbox.shortcut_details")}
            </Text>
          </Group>
          <Group gap={4}>
            <Kbd size="xs">E</Kbd>
            <Text size="xs" c="dimmed">
              {t("inbox.shortcut_close")}
            </Text>
          </Group>
        </Group>
      </Stack>
    </Center>
  )
}
