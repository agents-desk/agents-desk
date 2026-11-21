import { ActionIcon, Box, Group, Text, Textarea } from "@mantine/core"
import { IconSend } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"

/** ComposerProps 是输入框的设置。 */
interface ComposerProps {
  color: string
  touch: boolean // 手机和 App：回车换行，用按钮发送；电脑：回车发送，Shift + 回车换行
  placeholder: string
  onSend: (text: string) => void
}

/** Composer 是底部的输入框和发送按钮。字号 16px，iOS 点进去时页面不会放大；底部留出横条的安全区域。 */
export function Composer({ color, touch, placeholder, onSend }: ComposerProps): ReactNode {
  const t = useT()
  const [text, setText] = useState("")
  const send = () => {
    const value = text.trim()
    if (!value) return
    onSend(value)
    setText("")
  }
  return (
    <Box className="w-composer w-safe-bottom" style={{ flex: "none" }}>
      <Group gap="xs" align="flex-end" wrap="nowrap" p="xs">
        <Textarea
          value={text}
          onChange={(e) => setText(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (!touch && e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault()
              send()
            }
          }}
          placeholder={placeholder}
          autosize
          minRows={1}
          maxRows={5}
          radius="xl"
          size="md"
          maxLength={10000}
          style={{ flex: 1 }}
          aria-label={placeholder}
        />
        <ActionIcon size={42} radius="xl" color={color} variant="filled" disabled={!text.trim()} onClick={send} aria-label={t("composer.send")}>
          <IconSend size={18} />
        </ActionIcon>
      </Group>
      {!touch && (
        <Text size="xs" c="dimmed" ta="center" pb={6}>
          {t("composer.hint")}
        </Text>
      )}
    </Box>
  )
}
