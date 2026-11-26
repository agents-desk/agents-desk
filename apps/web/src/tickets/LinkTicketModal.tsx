import { Badge, Box, Center, Group, Loader, Modal, ScrollArea, Stack, Text, TextInput, UnstyledButton } from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconLink, IconLock, IconSearch } from "@tabler/icons-react"
import { notifyError, notifySuccess, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { ticketStatusColor, ticketStatusLabel } from "./meta"

/** LinkTicketModalProps 是将服务型会话关联到已有工单弹窗的设置。 */
interface LinkTicketModalProps {
  opened: boolean
  conversationId: string
  linkedTicketIds: Set<string> // 已关联的工单 ID，列表中自动排除
  onClose: () => void
}

/**
 * LinkTicketModal 让坐席按 #编号 或标题搜索可见的已有工单，并将当前服务型会话一键关联进去，
 * 把多个遇到同类问题的客户会话归集到同一张工单统一协作和答复。
 */
export function LinkTicketModal({ opened, conversationId, linkedTicketIds, onClose }: LinkTicketModalProps): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [q, setQ] = useState("")
  const [debounced] = useDebouncedValue(q.trim(), 200)

  useEffect(() => {
    if (opened) setQ("")
  }, [opened])

  const search = useQuery({
    queryKey: [...kit.queries.ticket.key(), "link-search", debounced],
    queryFn: () => tenantApi.ticket.list({ q: debounced || undefined, per_page: 25 }),
    enabled: opened,
  })

  const link = useMutation({
    mutationFn: (ticketId: string) => tenantApi.ticket.linkAdd(ticketId, { conversation_id: conversationId }),
    onSuccess: (updated) => {
      notifySuccess(t("tickets.link_ticket_done", { number: updated.number }))
      void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      onClose()
    },
    onError: (err) => notifyError(err),
  })

  const candidates = (search.data?.items ?? []).filter((tk) => !linkedTicketIds.has(tk.id))

  return (
    <Modal opened={opened} onClose={onClose} title={t("tickets.link_ticket_title")} size="md">
      <Stack gap="sm">
        <TextInput
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
          placeholder={t("tickets.link_ticket_placeholder")}
          leftSection={<IconSearch size={15} />}
          data-autofocus
        />
        <ScrollArea.Autosize mah={360}>
          {search.isPending ? (
            <Center py="lg">
              <Loader size="xs" />
            </Center>
          ) : candidates.length === 0 ? (
            <Text size="sm" c="dimmed" ta="center" py="lg">
              {t("tickets.link_ticket_empty")}
            </Text>
          ) : (
            <Stack gap={4}>
              {candidates.map((tk) => (
                <UnstyledButton
                  key={tk.id}
                  disabled={link.isPending}
                  onClick={() => link.mutate(tk.id)}
                  className="inbox-history"
                  px="xs"
                  py={8}
                  style={{ borderRadius: "var(--mantine-radius-sm)", border: "1px solid var(--tn-border)" }}
                >
                  <Group gap={6} wrap="nowrap">
                    <Text size="xs" fw={600} c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                      #{tk.number}
                    </Text>
                    <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                      {tk.title}
                    </Text>
                    {tk.visibility === "members" && <IconLock size={13} color="var(--tn-muted)" style={{ flex: "none" }} />}
                    <Badge size="xs" variant="light" color={ticketStatusColor[tk.status]} style={{ flex: "none" }}>
                      {ticketStatusLabel(tk.status, t)}
                    </Badge>
                    <IconLink size={14} color="var(--tn-muted)" style={{ flex: "none" }} />
                  </Group>
                  <Group gap={6} mt={3} wrap="nowrap">
                    <Text size="xs" c="dimmed" truncate>
                      {tk.type_name}
                    </Text>
                    <Text size="xs" c="dimmed">
                      ·
                    </Text>
                    <Text size="xs" c="dimmed">
                      {t("tickets.linked_count", { n: tk.linked_count })}
                    </Text>
                  </Group>
                </UnstyledButton>
              ))}
            </Stack>
          )}
        </ScrollArea.Autosize>
      </Stack>
    </Modal>
  )
}
