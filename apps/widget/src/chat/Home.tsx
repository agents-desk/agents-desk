import type { ConversationContactConversationDTO } from "@agents-desk/client/widget"
import { Badge, Box, Button, Group, Paper, Stack, Text, ThemeIcon, UnstyledButton } from "@mantine/core"
import { IconChevronRight, IconHeadset, IconPlus } from "@tabler/icons-react"
import { useLanguage, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { ago } from "./format"
import { promptText } from "./Prompt"

/** HomeProps 是首页的内容。 */
interface HomeProps {
  color: string
  name: string // 聊天插件的名称：系统发的消息显示它
  conversations: ConversationContactConversationDTO[]
  onOpen: (id: string) => void
  onNew: () => void
}

/**
 * Home 是有会话时的首页：问候语下方的“我的会话”，最近有消息的在前，每个会话显示最后一条消息是谁发的、文字和时间，
 * 有未读时标出红点，已关闭的标出；底部是“发起新会话”。还没有会话时首页是 FirstMessage。
 */
export function Home({ color, name, conversations, onOpen, onNew }: HomeProps): ReactNode {
  const t = useT()
  const language = useLanguage()
  return (
    <Stack gap="md" p="md" style={{ flex: 1, minHeight: 0 }}>
      <Paper withBorder radius="lg" mt={-30} style={{ overflow: "hidden", flex: "0 1 auto", minHeight: 0, display: "flex", flexDirection: "column" }}>
        <Text size="sm" c="dimmed" px="md" pt="sm" pb={4}>
          {t("home.my_conversations")}
        </Text>
        <Box style={{ overflowY: "auto" }}>
          {conversations.map((c) => {
            const who =
              c.preview_author.type === "contact" ? t("chat.me") : c.preview_author.type === "member" ? c.preview_author.name : name
            return (
              <UnstyledButton key={c.id} onClick={() => onOpen(c.id)} className="w-conversation" px="md" py="sm" w="100%">
                <Group gap="sm" wrap="nowrap">
                  <ThemeIcon size={36} radius="xl" variant="light" color={color}>
                    <IconHeadset size={20} />
                  </ThemeIcon>
                  <Box style={{ flex: 1, minWidth: 0 }}>
                    <Group gap={6} wrap="nowrap">
                      <Text size="sm" fw={600} truncate>
                        {who}
                      </Text>
                      {c.unread > 0 && <Box w={8} h={8} bg="red" style={{ borderRadius: "50%", flex: "none" }} aria-label={t("home.unread", { n: c.unread })} />}
                      {c.status === "closed" && (
                        <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                          {t("chat.status_closed")}
                        </Badge>
                      )}
                      <Text size="xs" c="dimmed" ml="auto" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                        {ago(c.last_message_at, language, t)}
                      </Text>
                    </Group>
                    <Text size="sm" c={c.unread > 0 ? undefined : "dimmed"} fw={c.unread > 0 ? 500 : undefined} truncate>
                      {promptText(c.preview_prompt, c.preview, language, t)}
                    </Text>
                  </Box>
                  <IconChevronRight size={16} className="w-conversation-arrow" style={{ flex: "none" }} />
                </Group>
              </UnstyledButton>
            )
          })}
        </Box>
      </Paper>
      <Button mt="auto" size="md" radius="md" fullWidth color={color} leftSection={<IconPlus size={18} />} onClick={onNew} style={{ flex: "none" }}>
        {t("home.new_conversation")}
      </Button>
    </Stack>
  )
}

/** FirstMessage 是新会话还没有消息时的提示，贴在输入框上方，发第一条就建会话。 */
export function FirstMessage(): ReactNode {
  const t = useT()
  return (
    <Paper withBorder radius="lg" p="md">
      <Text fw={600}>{t("home.first_title")}</Text>
      <Text size="sm" c="dimmed">
        {t("home.first_hint")}
      </Text>
    </Paper>
  )
}
