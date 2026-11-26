import type { ContactDTO, ContactSummary } from "@agents-desk/client/tenant"
import { Badge, Box, Button, Group, Loader, Modal, Paper, ScrollArea, Stack, Text, TextInput, UnstyledButton } from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconGitMerge, IconSearch } from "@tabler/icons-react"
import { notifyError, notifySuccess, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { ContactAvatar, contactDisplayName } from "./display"

/** MergeContactTarget 是保留的目标联系人最小字段集合，兼容 ContactDetailDTO 与 ContactSummary。 */
type MergeContactTarget = Pick<ContactDTO, "id" | "number" | "kind" | "name" | "email"> & { external_user_id?: string }

/**
 * MergeContactModal 是手动合并联系人的弹窗：支持按姓名/邮箱/编号搜索同工作区其他联系人，
 * 选定后把来源联系人的资料、标签、渠道身份和历史会话全部并入 target，默认同时把两者进行中的服务型会话合流为一条。
 */
export function MergeContactModal({
  opened,
  target,
  onClose,
  onMerged,
}: {
  opened: boolean
  target: MergeContactTarget | undefined
  onClose: () => void
  onMerged?: () => void
}): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [keyword, setKeyword] = useState("")
  const [debounced] = useDebouncedValue(keyword.trim(), 200)
  const [selected, setSelected] = useState<ContactDTO | null>(null)

  useEffect(() => {
    if (!opened) {
      setKeyword("")
      setSelected(null)
    }
  }, [opened])

  const candidates = useQuery({
    queryKey: [...kit.queries.contact.key(), "merge-candidates", target?.id ?? "", debounced],
    queryFn: () => tenantApi.contact.list({ q: debounced || undefined, per_page: 20, sort: "recent" }),
    enabled: opened && Boolean(target?.id),
  })

  const merge = useMutation({
    mutationFn: () => {
      if (!target || !selected) throw new Error("missing contact")
      return tenantApi.contact.merge(target.id, {
        source_id: selected.id,
      })
    },
    onSuccess: () => {
      notifySuccess(t("contacts.merge_done"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      onMerged?.()
      onClose()
    },
    onError: (err) => notifyError(err),
  })

  if (!target) return null
  const targetName = contactDisplayName(target as ContactSummary, t)
  const items = (candidates.data?.items ?? []).filter((c) => {
    if (c.id === target.id) return false
    // 两个联系人分别绑定了不同的外部业务用户 ID 时不能直接合并
    if (target.external_user_id && c.external_user_id && target.external_user_id !== c.external_user_id) return false
    return true
  })

  return (
    <Modal opened={opened} onClose={onClose} title={t("contacts.merge_title")} size="md" centered>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t("contacts.merge_desc", { name: targetName })}
        </Text>
        <TextInput
          value={keyword}
          onChange={(e) => setKeyword(e.currentTarget.value)}
          placeholder={t("contacts.merge_search")}
          leftSection={<IconSearch size={14} />}
          data-autofocus
        />
        <Paper withBorder radius="md" p={4}>
          <ScrollArea.Autosize mah={240}>
            {candidates.isPending ? (
              <Group justify="center" py="lg">
                <Loader size="xs" />
              </Group>
            ) : items.length === 0 ? (
              <Text size="xs" c="dimmed" ta="center" py="lg">
                {t("contacts.merge_empty")}
              </Text>
            ) : (
              <Stack gap={2}>
                {items.map((item) => {
                  const active = selected?.id === item.id
                  const itemName = contactDisplayName(item, t)
                  return (
                    <UnstyledButton
                      key={item.id}
                      onClick={() => setSelected(item)}
                      px="xs"
                      py={7}
                      style={{
                        borderRadius: "var(--mantine-radius-sm)",
                        backgroundColor: active ? "var(--mantine-primary-color-light)" : undefined,
                      }}
                    >
                      <Group gap="sm" wrap="nowrap">
                        <ContactAvatar contact={item} size={28} />
                        <Box style={{ flex: 1, minWidth: 0 }}>
                          <Group gap={6} wrap="nowrap">
                            <Text size="sm" fw={500} truncate>
                              {itemName}
                            </Text>
                            <Badge size="xs" variant="light" color={item.kind === "user" ? "bright-blue" : "gray"} style={{ flex: "none" }}>
                              {kit.label("ContactKind", item.kind)}
                            </Badge>
                          </Group>
                          <Text size="xs" c="dimmed" truncate>
                            {[item.email, item.external_user_id, `#${item.number}`].filter(Boolean).join(" · ")}
                          </Text>
                        </Box>
                        <Text size="xs" c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                          {item.conversation_count}
                        </Text>
                      </Group>
                    </UnstyledButton>
                  )
                })}
              </Stack>
            )}
          </ScrollArea.Autosize>
        </Paper>

        {selected && (
          <Paper withBorder radius="md" px="sm" py="xs" bg="var(--tn-canvas)">
            <Group gap={6} wrap="nowrap">
              <IconGitMerge size={14} style={{ flex: "none" }} />
              <Text size="xs">
                {t("contacts.merge_selected", {
                  source: contactDisplayName(selected, t),
                  count: selected.conversation_count,
                  target: targetName,
                })}
              </Text>
            </Group>
          </Paper>
        )}

        <Group justify="flex-end" mt="xs">
          <Button variant="default" onClick={onClose} disabled={merge.isPending}>
            {t("common.cancel")}
          </Button>
          <Button
            leftSection={<IconGitMerge size={14} />}
            disabled={!selected}
            loading={merge.isPending}
            onClick={() => merge.mutate()}
          >
            {t("contacts.merge_confirm_btn")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
