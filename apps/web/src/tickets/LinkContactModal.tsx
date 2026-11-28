import { Badge, Center, Group, Loader, Modal, ScrollArea, Stack, Text, TextInput, UnstyledButton } from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconLink, IconSearch } from "@tabler/icons-react"
import { notifyError, notifySuccess, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { contactDisplayName, contactKindColor } from "../contacts/display"

/** LinkContactModalProps 是在工单页关联受影响客户弹窗的设置。 */
interface LinkContactModalProps {
  opened: boolean
  ticketId: string
  linkedContactIds: Set<string>
  onClose: () => void
}

/**
 * LinkContactModal 让成员在工单详情页搜索并选择受影响客户，一键关联到当前工单统一协作与进度通知。
 */
export function LinkContactModal({ opened, ticketId, linkedContactIds, onClose }: LinkContactModalProps): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [q, setQ] = useState("")
  const [debounced] = useDebouncedValue(q.trim(), 200)

  useEffect(() => {
    if (opened) setQ("")
  }, [opened])

  const contacts = useQuery({
    queryKey: [...kit.queries.contact.key(), "ticket-link-search", debounced],
    queryFn: () => tenantApi.contact.list({ q: debounced || undefined, per_page: 25 }),
    enabled: opened,
  })

  const link = useMutation({
    mutationFn: (contactId: string) => tenantApi.ticket.contactAdd(ticketId, { contact_id: contactId }),
    onSuccess: () => {
      notifySuccess(t("tickets.link_contact_done"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      onClose()
    },
    onError: (err) => notifyError(err),
  })

  const candidates = (contacts.data?.items ?? []).filter((ct) => !linkedContactIds.has(ct.id))

  return (
    <Modal opened={opened} onClose={onClose} title={t("tickets.link_contact_title")} size="md" closeOnClickOutside={false}>
      <Stack gap="sm">
        <TextInput
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
          placeholder={t("tickets.link_contact_placeholder")}
          leftSection={<IconSearch size={16} />}
          data-autofocus
        />
        <ScrollArea.Autosize mah={360}>
          {contacts.isPending ? (
            <Center py="lg">
              <Loader size="xs" />
            </Center>
          ) : candidates.length === 0 ? (
            <Text size="sm" c="dimmed" ta="center" py="lg">
              {t("tickets.link_contact_empty")}
            </Text>
          ) : (
            <Stack gap={4}>
              {candidates.map((ct) => (
                <UnstyledButton
                  key={ct.id}
                  disabled={link.isPending}
                  onClick={() => link.mutate(ct.id)}
                  className="inbox-history"
                  px="xs"
                  py={8}
                  style={{ borderRadius: "var(--mantine-radius-sm)", border: "1px solid var(--tn-border)" }}
                >
                  <Group gap={6} wrap="nowrap">
                    <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                      {contactDisplayName(ct, t)}
                    </Text>
                    <Badge size="xs" variant="light" color={contactKindColor[ct.kind]} style={{ flex: "none" }}>
                      {kit.label("ContactKind", ct.kind)}
                    </Badge>
                    <IconLink size={14} color="var(--tn-muted)" style={{ flex: "none" }} />
                  </Group>
                  {ct.email && (
                    <Text size="xs" c="dimmed" truncate mt={2}>
                      {ct.email}
                    </Text>
                  )}
                </UnstyledButton>
              ))}
            </Stack>
          )}
        </ScrollArea.Autosize>
      </Stack>
    </Modal>
  )
}
