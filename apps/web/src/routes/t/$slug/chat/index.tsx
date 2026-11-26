import { Button, Center, Stack, Text, ThemeIcon } from "@mantine/core"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconMessageCircle, IconPlus } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import { useState } from "react"
import { NewChatModal } from "../../../../chat/NewChatModal"

// 尚未打开聊天时的空态引导页。
export const Route = createFileRoute("/t/$slug/chat/")({ component: NoChat })

function NoChat() {
  const t = useT()
  const { slug } = Route.useParams()
  const navigate = useNavigate()
  const [newOpen, setNewOpen] = useState(false)

  return (
    <Center style={{ flex: 1, background: "var(--tn-canvas)" }}>
      <Stack gap="sm" align="center" px="md">
        <ThemeIcon size={48} radius="xl" variant="light" color="gray">
          <IconMessageCircle size={24} stroke={1.5} />
        </ThemeIcon>
        <Stack gap={4} align="center">
          <Text fw={500} size="sm">
            {t("chat.pick")}
          </Text>
          <Text c="dimmed" size="xs" ta="center">
            {t("chat.pick_hint")}
          </Text>
        </Stack>
        <Button size="xs" variant="light" leftSection={<IconPlus size={14} />} onClick={() => setNewOpen(true)}>
          {t("chat.new")}
        </Button>
      </Stack>
      <NewChatModal
        opened={newOpen}
        onClose={() => setNewOpen(false)}
        onOpened={(chat) => void navigate({ to: "/t/$slug/chat/$id", params: { slug, id: chat.id } })}
      />
    </Center>
  )
}
