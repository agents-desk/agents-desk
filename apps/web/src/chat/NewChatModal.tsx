import type { ConversationChatDTO } from "@agents-desk/client/tenant"
import {
  Avatar,
  Button,
  Checkbox,
  Group,
  Indicator,
  Modal,
  ScrollArea,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  UnstyledButton,
} from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconSearch, IconUsersGroup } from "@tabler/icons-react"
import { notifyError, useT } from "@go-tenon/ui"
import { type FormEvent, type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { useMe, useMembers } from "../inbox/data"

/** NewChatModalProps 是发起单聊或新建群聊弹窗的设置。 */
interface NewChatModalProps {
  opened: boolean
  onClose: () => void
  onOpened: (chat: ConversationChatDTO) => void
}

/**
 * NewChatModal 提供统一的“发起聊天”弹窗：
 * - 单聊模式：搜索同事并点击立即打开或创建唯一单聊；
 * - 群聊模式：填写群名称并勾选成员，一键建群并进入群聊。
 */
export function NewChatModal({ opened, onClose, onOpened }: NewChatModalProps): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()

  const [tab, setTab] = useState<"direct" | "group">("direct")
  const [q, setQ] = useState("")
  const [groupName, setGroupName] = useState("")
  const [selected, setSelected] = useState<string[]>([])
  const [nameError, setNameError] = useState<string | null>(null)

  useEffect(() => {
    if (!opened) return
    setTab("direct")
    setQ("")
    setGroupName("")
    setSelected([])
    setNameError(null)
  }, [opened])

  const finish = (chat: ConversationChatDTO) => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
    onClose()
    onOpened(chat)
  }

  const startDirect = useMutation({
    mutationFn: (memberId: string) => tenantApi.chat.createDirect({ participant_id: memberId }),
    onSuccess: finish,
    onError: (err) => notifyError(err),
  })

  const startGroup = useMutation({
    mutationFn: () =>
      tenantApi.chat.createGroup({
        name: groupName.trim(),
        member_ids: selected,
      }),
    onSuccess: finish,
    onError: (err) => notifyError(err),
  })

  const kw = q.trim().toLowerCase()
  const colleagues = members.filter(
    (m) => m.id !== me.data?.id && (!kw || m.name.toLowerCase().includes(kw) || m.email.toLowerCase().includes(kw)),
  )

  const toggleMember = (id: string) => {
    setSelected((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
  }

  const submitGroup = (e: FormEvent) => {
    e.preventDefault()
    if (!groupName.trim()) {
      setNameError(t("chat.group_name_required"))
      return
    }
    setNameError(null)
    startGroup.mutate()
  }

  return (
    <Modal opened={opened} onClose={onClose} title={t("chat.new_title")} size="md">
      <Stack gap="sm">
        <SegmentedControl
          fullWidth
          value={tab}
          onChange={(v) => setTab(v as "direct" | "group")}
          data={[
            { value: "direct", label: t("chat.mode_direct") },
            { value: "group", label: t("chat.mode_group") },
          ]}
        />

        {tab === "group" && (
          <TextInput
            label={t("chat.group_name")}
            placeholder={t("chat.group_name_placeholder")}
            value={groupName}
            onChange={(e) => {
              setGroupName(e.currentTarget.value)
              if (nameError) setNameError(null)
            }}
            error={nameError ?? undefined}
            maxLength={80}
            required
            data-autofocus
          />
        )}

        <TextInput
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
          placeholder={t("chat.pick_member_placeholder")}
          leftSection={<IconSearch size={15} />}
          data-autofocus={tab === "direct" ? true : undefined}
        />

        <ScrollArea.Autosize mah={320}>
          {colleagues.length === 0 ? (
            <Text size="sm" c="dimmed" ta="center" py="lg">
              {t("chat.no_colleagues")}
            </Text>
          ) : (
            <Stack gap={4}>
              {colleagues.map((m) =>
                tab === "direct" ? (
                  <UnstyledButton
                    key={m.id}
                    disabled={startDirect.isPending}
                    onClick={() => startDirect.mutate(m.id)}
                    className="inbox-history"
                    px="xs"
                    py={8}
                    style={{ borderRadius: "var(--mantine-radius-sm)" }}
                  >
                    <Group gap="sm" wrap="nowrap">
                      <Indicator size={8} offset={2} position="bottom-end" color={m.status === "online" ? "green" : "gray"} withBorder>
                        <Avatar size={30} radius="xl" name={m.name} color="initials" src={m.avatar_url || undefined} />
                      </Indicator>
                      <Stack gap={1} style={{ flex: 1, minWidth: 0 }}>
                        <Text size="sm" fw={500} truncate>
                          {m.name}
                        </Text>
                        <Text size="xs" c="dimmed" truncate>
                          {m.role_name} · {m.email}
                        </Text>
                      </Stack>
                    </Group>
                  </UnstyledButton>
                ) : (
                  <UnstyledButton
                    key={m.id}
                    onClick={() => toggleMember(m.id)}
                    className="inbox-history"
                    px="xs"
                    py={8}
                    style={{ borderRadius: "var(--mantine-radius-sm)" }}
                  >
                    <Group gap="sm" wrap="nowrap">
                      <Checkbox checked={selected.includes(m.id)} onChange={() => {}} tabIndex={-1} style={{ pointerEvents: "none" }} />
                      <Avatar size={28} radius="xl" name={m.name} color="initials" src={m.avatar_url || undefined} />
                      <Stack gap={1} style={{ flex: 1, minWidth: 0 }}>
                        <Text size="sm" fw={500} truncate>
                          {m.name}
                        </Text>
                        <Text size="xs" c="dimmed" truncate>
                          {m.role_name}
                        </Text>
                      </Stack>
                    </Group>
                  </UnstyledButton>
                ),
              )}
            </Stack>
          )}
        </ScrollArea.Autosize>

        {tab === "group" && (
          <form onSubmit={submitGroup}>
            <Group justify="space-between" align="center" mt="xs">
              <Text size="xs" c="dimmed">
                {t("chat.selected_members", { n: selected.length + 1 })}
              </Text>
              <Group gap="xs">
                <Button variant="default" onClick={onClose} disabled={startGroup.isPending}>
                  {t("common.cancel")}
                </Button>
                <Button type="submit" leftSection={<IconUsersGroup size={15} />} loading={startGroup.isPending}>
                  {t("chat.create_group")}
                </Button>
              </Group>
            </Group>
          </form>
        )}
      </Stack>
    </Modal>
  )
}
