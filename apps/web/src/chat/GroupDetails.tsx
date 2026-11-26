import type { ConversationChatDTO } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Avatar,
  Badge,
  Button,
  Divider,
  Group,
  Indicator,
  MultiSelect,
  Stack,
  Text,
  TextInput,
  Tooltip,
} from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconLogout, IconUserMinus, IconUserPlus, IconX } from "@tabler/icons-react"
import { confirm, notifyError, notifySuccess, PaneBody, PaneHeader, useT } from "@go-tenon/ui"
import { type FormEvent, type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { useMe, useMembers } from "../inbox/data"

/** GroupDetailsProps 是群聊右侧详情栏的设置。 */
interface GroupDetailsProps {
  chat: ConversationChatDTO
  onClose: () => void
  onLeft: () => void
}

/**
 * GroupDetails 是群聊的右侧内联详情栏内容：支持修改群名称、查看成员列表、邀请新成员、移出成员以及退出群聊。
 */
export function GroupDetails({ chat, onClose, onLeft }: GroupDetailsProps): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: allMembers } = useMembers()

  const [name, setName] = useState(chat.name)
  const [toAdd, setToAdd] = useState<string[]>([])

  useEffect(() => {
    setName(chat.name)
    setToAdd([])
  }, [chat.id, chat.name])

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
    void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "timeline", chat.id] })
  }

  const rename = useMutation({
    mutationFn: () => tenantApi.chat.update(chat.id, { name: name.trim() }),
    onSuccess: () => {
      notifySuccess(t("common.saved"))
      invalidate()
    },
    onError: (err) => notifyError(err),
  })

  const addMembers = useMutation({
    mutationFn: () => tenantApi.chat.participantsAdd(chat.id, { member_ids: toAdd }),
    onSuccess: () => {
      setToAdd([])
      notifySuccess(t("chat.members_added"))
      invalidate()
    },
    onError: (err) => notifyError(err),
  })

  const removeMember = useMutation({
    mutationFn: (memberId: string) => tenantApi.chat.participantRemove(chat.id, "member", memberId),
    onSuccess: () => {
      notifySuccess(t("chat.member_removed"))
      invalidate()
    },
    onError: (err) => notifyError(err),
  })

  const leave = useMutation({
    mutationFn: () => tenantApi.chat.leave(chat.id),
    onSuccess: () => {
      notifySuccess(t("chat.left_group"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
      onClose()
      onLeft()
    },
    onError: (err) => notifyError(err),
  })

  const participants = chat.participants ?? []
  const existingIds = new Set(participants.filter((p) => p.type === "member").map((p) => p.id))
  const addable = allMembers
    .filter((m) => !existingIds.has(m.id))
    .map((m) => ({ value: m.id, label: m.name }))

  const onSaveName = (e: FormEvent) => {
    e.preventDefault()
    if (!name.trim() || name.trim() === chat.name) return
    rename.mutate()
  }

  const handleRemove = async (memberId: string, memberName: string) => {
    const ok = await confirm({
      title: t("chat.remove_member_confirm", { name: memberName }),
      message: t("chat.remove_member_message"),
      danger: true,
      confirmLabel: t("chat.remove_member"),
    })
    if (ok) removeMember.mutate(memberId)
  }

  const handleLeave = async () => {
    const ok = await confirm({
      title: t("chat.leave_confirm"),
      message: t("chat.leave_message"),
      danger: true,
      confirmLabel: t("chat.leave"),
    })
    if (ok) leave.mutate()
  }

  return (
    <Stack gap={0} h="100%">
      <PaneHeader
        title={t("chat.group_settings")}
        actions={
          <ActionIcon variant="subtle" color="gray" size="sm" aria-label={t("inbox.close_details")} onClick={onClose}>
            <IconX size={16} />
          </ActionIcon>
        }
      />

      <PaneBody>
        <Stack gap="md" p="var(--tn-pane-px)">
          <form onSubmit={onSaveName}>
            <Stack gap="xs">
              <TextInput
                label={t("chat.group_name")}
                value={name}
                onChange={(e) => setName(e.currentTarget.value)}
                maxLength={80}
              />
              {name.trim() && name.trim() !== chat.name && (
                <Group justify="flex-end">
                  <Button size="compact-xs" type="submit" loading={rename.isPending}>
                    {t("common.save")}
                  </Button>
                </Group>
              )}
            </Stack>
          </form>

          <Divider />

          {addable.length > 0 && (
            <Stack gap="xs">
              <MultiSelect
                label={t("chat.add_members")}
                placeholder={t("chat.pick_member_placeholder")}
                value={toAdd}
                onChange={setToAdd}
                data={addable}
                searchable
                clearable
              />
              {toAdd.length > 0 && (
                <Group justify="flex-end">
                  <Button size="compact-xs" leftSection={<IconUserPlus size={13} />} loading={addMembers.isPending} onClick={() => addMembers.mutate()}>
                    {t("chat.add_members_submit")}
                  </Button>
                </Group>
              )}
            </Stack>
          )}

          <Stack gap="xs">
            <Text size="xs" fw={600} c="dimmed">
              {t("chat.members_count", { n: participants.length })}
            </Text>
            <Stack gap={8}>
              {participants.map((p) => {
                const isMe = p.type === "member" && p.id === me.data?.id
                return (
                  <Group key={`${p.type}:${p.id}`} gap="sm" wrap="nowrap">
                    <Indicator
                      size={8}
                      offset={2}
                      position="bottom-end"
                      color={p.status === "online" ? "green" : "gray"}
                      withBorder
                      disabled={p.type !== "member"}
                    >
                      <Avatar size={28} radius="xl" name={p.name || p.id} color="initials" src={p.avatar_url || undefined} />
                    </Indicator>
                    <Group gap={6} wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
                      <Text size="sm" truncate>
                        {p.name || p.id}
                      </Text>
                      {isMe && (
                        <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                          {t("inbox.you")}
                        </Badge>
                      )}
                    </Group>
                    {!isMe && p.type === "member" && (
                      <Tooltip label={t("chat.remove_member")} openDelay={250}>
                        <ActionIcon
                          size={24}
                          variant="subtle"
                          color="gray"
                          aria-label={t("chat.remove_member")}
                          disabled={removeMember.isPending}
                          onClick={() => void handleRemove(p.id, p.name)}
                        >
                          <IconUserMinus size={14} />
                        </ActionIcon>
                      </Tooltip>
                    )}
                  </Group>
                )
              })}
            </Stack>
          </Stack>

          <Divider />

          <Button variant="light" color="red" leftSection={<IconLogout size={15} />} loading={leave.isPending} onClick={() => void handleLeave()}>
            {t("chat.leave")}
          </Button>
        </Stack>
      </PaneBody>
    </Stack>
  )
}
