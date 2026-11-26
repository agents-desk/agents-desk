import type { ConversationDTO } from "@agents-desk/client/tenant"
import { Badge, Center, Group, Loader, Modal, ScrollArea, Stack, Text, TextInput, UnstyledButton } from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconLink, IconSearch } from "@tabler/icons-react"
import { notifyError, notifySuccess, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { contactDisplayName } from "../contacts/display"
import { previewSummary } from "../inbox/Attachments"
import { ChannelIcon } from "../inbox/channel-badge"
import { activity } from "../inbox/format"

/** LinkConversationModalProps 是在工单页关联服务型会话弹窗的设置。 */
interface LinkConversationModalProps {
  opened: boolean
  ticketId: string
  linkedConversationIds: Set<string>
  onClose: () => void
}

/**
 * LinkConversationModal 让成员在工单详情页搜索并选择可见的服务型会话，一键关联归集到当前工单。
 */
export function LinkConversationModal({ opened, ticketId, linkedConversationIds, onClose }: LinkConversationModalProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const queryClient = useQueryClient()
  const [q, setQ] = useState("")
  const [debounced] = useDebouncedValue(q.trim(), 200)

  useEffect(() => {
    if (opened) setQ("")
  }, [opened])

  const recent = useQuery({
    queryKey: [...kit.queries.conversation.key(), "ticket-link-recent"],
    queryFn: () => tenantApi.conversation.list({ inbox: "all", sort: "recent", per_page: 30 }),
    enabled: opened && !debounced,
  })

  const searched = useQuery({
    queryKey: [...kit.queries.conversation.key(), "ticket-link-search", debounced],
    queryFn: () => tenantApi.conversation.search({ q: debounced, per_page: 25 }),
    enabled: opened && debounced.length > 0,
  })

  const link = useMutation({
    mutationFn: (conversationId: string) => tenantApi.ticket.linkAdd(ticketId, { conversation_id: conversationId }),
    onSuccess: () => {
      notifySuccess(t("tickets.link_conversation_done"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      onClose()
    },
    onError: (err) => notifyError(err),
  })

  const rawList: ConversationDTO[] = debounced
    ? (searched.data?.items ?? []).map((h) => h.conversation)
    : (recent.data?.items ?? [])
  const seen = new Set<string>()
  const candidates = rawList.filter((c) => {
    if (linkedConversationIds.has(c.id) || seen.has(c.id)) return false
    seen.add(c.id)
    return true
  })
  const loading = debounced ? searched.isPending : recent.isPending

  return (
    <Modal opened={opened} onClose={onClose} title={t("tickets.link_conversation_title")} size="md">
      <Stack gap="sm">
        <TextInput
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
          placeholder={t("tickets.link_conversation_placeholder")}
          leftSection={<IconSearch size={15} />}
          data-autofocus
        />
        <ScrollArea.Autosize mah={360}>
          {loading ? (
            <Center py="lg">
              <Loader size="xs" />
            </Center>
          ) : candidates.length === 0 ? (
            <Text size="sm" c="dimmed" ta="center" py="lg">
              {t("tickets.link_conversation_empty")}
            </Text>
          ) : (
            <Stack gap={4}>
              {candidates.map((c) => (
                <UnstyledButton
                  key={c.id}
                  disabled={link.isPending}
                  onClick={() => link.mutate(c.id)}
                  className="inbox-history"
                  px="xs"
                  py={8}
                  style={{ borderRadius: "var(--mantine-radius-sm)", border: "1px solid var(--tn-border)" }}
                >
                  <Group gap={6} wrap="nowrap">
                    <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                      {contactDisplayName(c.contact, t)}
                    </Text>
                    {c.channel && (
                      <Badge size="xs" variant="light" color="gray" leftSection={<ChannelIcon kind={c.channel.kind} size={10} />} style={{ flex: "none", textTransform: "none" }}>
                        {c.channel.name}
                      </Badge>
                    )}
                    <Text size="xs" c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                      {activity(c.last_message_at, ui, t, zone)}
                    </Text>
                    <IconLink size={14} color="var(--tn-muted)" style={{ flex: "none" }} />
                  </Group>
                  <Text size="xs" c="dimmed" truncate mt={3}>
                    {previewSummary(c.preview, c.preview_attachment, t) || "—"}
                  </Text>
                </UnstyledButton>
              ))}
            </Stack>
          )}
        </ScrollArea.Autosize>
      </Stack>
    </Modal>
  )
}
