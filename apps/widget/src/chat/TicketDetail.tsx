import type { ConversationContactTicketDTO } from "@agents-desk/client/widget"
import { Alert, Badge, Box, Button, Center, Group, Loader, Paper, Stack, Text, ThemeIcon, UnstyledButton } from "@mantine/core"
import { IconAlertCircle, IconChevronRight, IconMessageCircle, IconTicket } from "@tabler/icons-react"
import { ImageLightbox, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { type LightboxImage, WidgetMessageAttachments } from "./Attachments"
import { ago, clock, dayLabel } from "./format"
import { Linkify } from "./linkify"
import { useTicket } from "./queries"

type T = ReturnType<typeof useT>

/** ticketStatusColor 是客户侧工单状态的语义色板。 */
export const ticketStatusColor: Record<ConversationContactTicketDTO["status"], string> = {
  submitted: "gray",
  in_progress: "bright-blue",
  waiting_on_customer: "yellow",
  resolved: "green",
}

/** ticketStatusLabel 返回按客户浏览器语言本地化的客户侧工单状态文案。 */
export function ticketStatusLabel(status: ConversationContactTicketDTO["status"], t: T): string {
  switch (status) {
    case "submitted":
      return t("tickets.status_submitted")
    case "in_progress":
      return t("tickets.status_in_progress")
    case "waiting_on_customer":
      return t("tickets.status_waiting_on_customer")
    case "resolved":
      return t("tickets.status_resolved")
  }
}

/** TicketListItems 渲染客户可见的工单列表项，供首页工单标签页和单会话工单视图共用。 */
export function TicketListItems({
  tickets,
  color,
  onOpen,
}: {
  tickets: ConversationContactTicketDTO[]
  color: string
  onOpen: (id: string) => void
}): ReactNode {
  const t = useT()
  const language = useLanguage()
  return (
    <Box style={{ overflowY: "auto" }}>
      {tickets.map((tk) => (
        <UnstyledButton key={tk.id} onClick={() => onOpen(tk.id)} className="w-conversation" px="md" py="sm" w="100%">
          <Group gap="sm" wrap="nowrap">
            <ThemeIcon size={36} radius="xl" variant="light" color={tk.status === "waiting_on_customer" ? "yellow" : color}>
              <IconTicket size={19} />
            </ThemeIcon>
            <Box style={{ flex: 1, minWidth: 0 }}>
              <Group gap={6} wrap="nowrap">
                <Text size="xs" fw={600} c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                  #{tk.number}
                </Text>
                <Badge size="xs" variant="light" color={ticketStatusColor[tk.status]} style={{ flex: "none" }}>
                  {ticketStatusLabel(tk.status, t)}
                </Badge>
                <Text size="xs" c="dimmed" ml="auto" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                  {ago(tk.last_message_at, language, t)}
                </Text>
              </Group>
              <Text size="sm" fw={600} truncate mt={2}>
                {tk.title}
              </Text>
              {tk.latest_update && (
                <Text size="xs" c="dimmed" truncate mt={1}>
                  {tk.latest_update}
                </Text>
              )}
            </Box>
            <IconChevronRight size={16} className="w-conversation-arrow" style={{ flex: "none" }} />
          </Group>
        </UnstyledButton>
      ))}
    </Box>
  )
}

/**
 * TicketDetailView 是客户侧单张工单详情页：
 * 展示工单编号、状态、标题、描述与公开进度记录；点击底部按钮直达关联会话补充文字或图片。
 */
export function TicketDetailView({
  id,
  color,
  name,
  onOpenConversation,
}: {
  id: string
  color: string
  name: string
  onOpenConversation: (conversationId: string) => void
}): ReactNode {
  const t = useT()
  const language = useLanguage()
  const ticket = useTicket(id)
  const [lightbox, setLightbox] = useState<LightboxImage | null>(null)

  if (ticket.isPending) {
    return (
      <Center style={{ flex: 1 }}>
        <Loader color={color} size="sm" />
      </Center>
    )
  }

  if (!ticket.data) {
    return (
      <Center style={{ flex: 1 }} p="xl">
        <Text size="sm" c="dimmed" ta="center">
          {t("tickets.not_found")}
        </Text>
      </Center>
    )
  }

  const tk = ticket.data
  const waiting = tk.status === "waiting_on_customer"

  return (
    <>
      <Box className="w-scroll">
        <Stack gap="sm" p="md">
          <Paper withBorder radius="lg" p="md">
            <Group gap={6} wrap="nowrap" mb={6}>
              <Text size="xs" fw={600} c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                #{tk.number}
              </Text>
              <Badge size="xs" variant="light" color={ticketStatusColor[tk.status]}>
                {ticketStatusLabel(tk.status, t)}
              </Badge>
              {tk.type_name && (
                <Text size="xs" c="dimmed" truncate ml="auto">
                  {tk.type_name}
                </Text>
              )}
            </Group>
            <Text size="md" fw={600} lh={1.35}>
              {tk.title}
            </Text>
            {tk.description && (
              <Text size="sm" c="dimmed" mt={6} style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                {tk.description}
              </Text>
            )}
          </Paper>

          {waiting && (
            <Alert color="yellow" radius="md" icon={<IconAlertCircle size={16} />}>
              <Text size="xs">{t("tickets.waiting_hint")}</Text>
            </Alert>
          )}

          <Text size="xs" fw={600} c="dimmed" px={4}>
            {t("tickets.updates_heading")}
          </Text>

          {tk.updates.length === 0 ? (
            <Paper withBorder radius="lg" p="md">
              <Text size="sm" c="dimmed" ta="center">
                {t("tickets.updates_empty")}
              </Text>
            </Paper>
          ) : (
            <Stack gap="xs">
              {tk.updates.map((u) => {
                const author = u.author.type === "member" && u.author.name ? u.author.name : name
                return (
                  <Paper key={u.id} withBorder radius="lg" p="sm">
                    <Group justify="space-between" gap="xs" mb={4} wrap="nowrap">
                      <Text size="xs" fw={600} truncate>
                        {author}
                      </Text>
                      <Text size="xs" c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                        {dayLabel(u.created_at, language, t)} · {clock(u.created_at, language)}
                      </Text>
                    </Group>
                    {u.text && (
                      <Text size="sm" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                        <Linkify text={u.text} />
                      </Text>
                    )}
                    <WidgetMessageAttachments attachments={u.attachments} hasText={Boolean(u.text)} mine={false} onPreview={setLightbox} />
                  </Paper>
                )
              })}
            </Stack>
          )}
        </Stack>
      </Box>

      {tk.conversation_id && (
        <Box p="sm" className="w-composer w-safe-bottom">
          <Button
            fullWidth
            size="md"
            radius="md"
            color={waiting ? "yellow" : color}
            leftSection={<IconMessageCircle size={17} />}
            onClick={() => onOpenConversation(tk.conversation_id)}
          >
            {t(waiting ? "tickets.reply_action" : "tickets.open_conversation")}
          </Button>
        </Box>
      )}
      <ImageLightbox src={lightbox?.src ?? null} filename={lightbox?.filename} onClose={() => setLightbox(null)} />
    </>
  )
}
