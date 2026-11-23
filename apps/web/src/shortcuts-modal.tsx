import { Divider, Group, Kbd, Modal, Stack, Text } from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { useQuery } from "@tanstack/react-query"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useEffect } from "react"
import { me } from "./auth"
import { isTypingTarget } from "./layout"

interface ShortcutRow {
  keys: string[]
  label: string
}

interface ShortcutGroup {
  title: string
  rows: ShortcutRow[]
}

/**
 * ShortcutsModal 是按 ? 唤起的快捷键帮助弹窗，分组展示全局、列表导航、会话操作和回复框快捷键，
 * 并根据操作系统（macOS / 其他）与成员发送偏好动态展示对应按键。
 */
export function ShortcutsModal({
  opened,
  onClose,
  onToggle,
}: {
  opened: boolean
  onClose: () => void
  onToggle: () => void
}): ReactNode {
  const t = useT()
  const os = useOs()
  const profile = useQuery({ queryKey: ["me"], queryFn: me })
  const isMac = os === "macos" || os === "ios"
  const mod = isMac ? "⌘" : "Ctrl"
  const ctrlEnterSend = profile.data?.send_shortcut === "ctrl_enter"

  // 全局非输入态按 ? 切换快捷键帮助面板
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || isTypingTarget(e.target)) return
      if (e.key === "?") {
        e.preventDefault()
        onToggle()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [onToggle])

  const groups: ShortcutGroup[] = [
    {
      title: t("shortcuts.global_group"),
      rows: [
        { keys: [mod, "K"], label: t("shortcuts.global_palette") },
        { keys: ["?"], label: t("shortcuts.global_help") },
      ],
    },
    {
      title: t("shortcuts.list_group"),
      rows: [
        { keys: ["J"], label: t("shortcuts.list_next") },
        { keys: ["K"], label: t("shortcuts.list_prev") },
        { keys: ["Shift", "J"], label: t("shortcuts.list_next_waiting") },
      ],
    },
    {
      title: t("shortcuts.conversation_group"),
      rows: [
        { keys: ["R"], label: t("shortcuts.conversation_reply") },
        { keys: ["N"], label: t("shortcuts.conversation_note") },
        { keys: ["I"], label: t("shortcuts.conversation_details") },
        { keys: ["E"], label: t("shortcuts.conversation_close") },
      ],
    },
    {
      title: t("shortcuts.composer_group"),
      rows: [
        { keys: [mod, "I"], label: t("shortcuts.composer_mode") },
        { keys: ["/"], label: t("shortcuts.composer_macro") },
        { keys: ["@"], label: t("shortcuts.composer_mention") },
        { keys: ctrlEnterSend ? [mod, "↵"] : ["↵"], label: t("shortcuts.composer_send") },
      ],
    },
  ]

  return (
    <Modal opened={opened} onClose={onClose} title={t("shortcuts.title")} size="md" radius="md">
      <Stack gap="md">
        {groups.map((group, idx) => (
          <Stack key={group.title} gap="xs">
            {idx > 0 && <Divider />}
            <Text size="xs" fw={600} c="dimmed">
              {group.title}
            </Text>
            <Stack gap={6}>
              {group.rows.map((row) => (
                <Group key={row.label} justify="space-between" wrap="nowrap">
                  <Text size="sm">{row.label}</Text>
                  <Group gap={4} wrap="nowrap">
                    {row.keys.map((k) => (
                      <Kbd key={k} size="xs">
                        {k}
                      </Kbd>
                    ))}
                  </Group>
                </Group>
              ))}
            </Stack>
          </Stack>
        ))}
        <Text size="xs" c="dimmed">
          {t("shortcuts.hint")}
        </Text>
      </Stack>
    </Modal>
  )
}
