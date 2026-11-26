import { Group, Kbd, Text } from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { createFileRoute } from "@tanstack/react-router"
import { IconMessages } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import { EmptyState } from "../../../../components/empty-state"

// 还没有打开会话时，会话一栏的提示与常用快捷键导览。
export const Route = createFileRoute("/t/$slug/inbox/")({ component: NoConversation })

function NoConversation() {
  const t = useT()
  const os = useOs()
  const key = os === "macos" || os === "ios" ? "⌘K" : "Ctrl+K"
  const shortcuts = [
    { keys: ["J", "K"], label: "inbox.shortcut_nav" },
    { keys: ["R"], label: "inbox.shortcut_reply" },
    { keys: ["I"], label: "inbox.shortcut_details" },
    { keys: ["E"], label: "inbox.shortcut_close" },
  ] as const
  return (
    <EmptyState
      fill
      icon={<IconMessages size={24} stroke={1.6} />}
      title={t("inbox.pick")}
      description={t("inbox.pick_shortcut", { key })}
      action={
        <Group gap="md" justify="center" mt={4}>
          {shortcuts.map((s) => (
            <Group key={s.label} gap={4}>
              {s.keys.map((k) => (
                <Kbd key={k} size="xs">
                  {k}
                </Kbd>
              ))}
              <Text size="xs" c="dimmed">
                {t(s.label)}
              </Text>
            </Group>
          ))}
        </Group>
      }
    />
  )
}
