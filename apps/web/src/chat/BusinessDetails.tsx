import type { ConversationChatDTO } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Avatar,
  Badge,
  Button,
  CopyButton,
  Divider,
  Group,
  Indicator,
  MultiSelect,
  Stack,
  Text,
  ThemeIcon,
  Tooltip,
} from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Link, useParams } from "@tanstack/react-router"
import {
  IconCheck,
  IconCopy,
  IconExternalLink,
  IconLogout,
  IconUserMinus,
  IconUserPlus,
  IconUsersGroup,
  IconX,
} from "@tabler/icons-react"
import { confirm, Inspector, InspectorField, InspectorSection, notifyError, notifySuccess, PaneBody, PaneHeader, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { ContactAvatar } from "../contacts/display"
import { channelColor, ChannelChip } from "../inbox/channel-badge"
import { useMe, useMembers } from "../inbox/data"
import { LanguageSelect } from "../LanguageSelect"
import { languageName } from "../languages"
import { chatDisplayName } from "./data"

/** AUTO 是对方语言中的“自动识别”：按收到的消息识别。 */
const AUTO = "auto"

/** BusinessDetailsProps 是商务沟通右侧详情栏的设置。 */
interface BusinessDetailsProps {
  chat: ConversationChatDTO
  onClose: () => void
  onLeft: () => void
}

/**
 * BusinessDetails 是外部商务沟通（1:1 商务单聊与外部 Telegram 群聊）的右侧内联详情栏：
 * 展示所属个人商务渠道、对话类型、对方语言设置（手动指定或自动识别）、对方联系人资料及主页入口，
 * 并支持邀请同事加入协作、移出协作成员或退出协助。
 */
export function BusinessDetails({ chat, onClose, onLeft }: BusinessDetailsProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const { slug } = useParams({ from: "/$slug" })
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: allMembers } = useMembers()

  const [toAdd, setToAdd] = useState<string[]>([])
  useEffect(() => {
    setToAdd([])
  }, [chat.id])

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
    void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "timeline", chat.id] })
    void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
  }

  const setLanguage = useMutation({
    mutationFn: (lang: string) => tenantApi.conversation.language(chat.id, { language: lang }),
    onSuccess: invalidate,
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

  const title = chatDisplayName(chat, t)
  const participants = chat.participants ?? []
  const memberParticipants = participants.filter((p) => p.type === "member")
  const contactParticipants = participants.filter((p) => p.type === "contact")
  const existingMemberIds = new Set(memberParticipants.map((p) => p.id))
  const addable = allMembers
    .filter((m) => !existingMemberIds.has(m.id))
    .map((m) => ({ value: m.id, label: m.name }))

  const manual = chat.contact?.language_manual ?? false
  const detected = chat.language ? languageName(chat.language, ui) : ""
  const canLeave = memberParticipants.length > 1
  const chColor = chat.channel ? channelColor(chat.channel.kind) : "brand"

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
    <Stack gap={0} h="100%" className="inbox-details">
      <PaneHeader
        title={t("chat.business_details")}
        actions={
          <ActionIcon variant="subtle" color="gray" size="sm" aria-label={t("inbox.close_details")} onClick={onClose}>
            <IconX size={16} />
          </ActionIcon>
        }
      />

      <PaneBody>
        <Stack gap="sm" px="var(--tn-pane-px)" pt="md" pb="sm" align="center">
          {chat.mode === "group" ? (
            <ThemeIcon size={52} radius="xl" variant="light" color={chColor}>
              <IconUsersGroup size={24} stroke={1.6} />
            </ThemeIcon>
          ) : (
            <ContactAvatar contact={chat.contact} size={52} />
          )}
          <Stack gap={6} align="center" w="100%">
            <Text size="md" fw={600} ta="center" truncate maw="100%">
              {title}
            </Text>
            <Group gap={6} justify="center" wrap="wrap">
              {chat.channel && <ChannelChip channel={chat.channel} />}
              <Badge size="xs" variant="light" color="gray">
                {t(chat.mode === "group" ? "chat.business_type_group" : "chat.business_type_direct")}
              </Badge>
            </Group>
          </Stack>
        </Stack>

        <Inspector>
          <InspectorSection title={t("chat.business_info")} collapsible storeKey="chat-business-info">
            <InspectorField label={t("inbox.contact_language")}>
              <LanguageSelect
                value={manual && chat.language ? chat.language : AUTO}
                before={[
                  {
                    value: AUTO,
                    label: detected ? t("inbox.language_auto_detected", { lang: detected }) : t("inbox.language_auto"),
                  },
                ]}
                first={chat.language ? [chat.language] : []}
                disabled={setLanguage.isPending}
                onChange={(v) => {
                  if (!v) return
                  setLanguage.mutate(v === AUTO ? "" : v)
                }}
              />
            </InspectorField>

            {chat.contact && (
              <>
                <InspectorField label={t("contacts.company")}>
                  {chat.contact.company ? (
                    <Text size="sm" truncate>
                      {chat.contact.company}
                    </Text>
                  ) : (
                    <Text size="sm" c="dimmed">
                      —
                    </Text>
                  )}
                </InspectorField>
                <InspectorField label={t("inbox.contact_email")}>
                  {chat.contact.email ? (
                    <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
                      <Text size="sm" truncate style={{ flex: 1, minWidth: 0 }}>
                        {chat.contact.email}
                      </Text>
                      <CopyButton value={chat.contact.email} timeout={1500}>
                        {({ copied, copy }) => (
                          <Tooltip label={t(copied ? "inbox.copied" : "chat.copy_email")} openDelay={250}>
                            <ActionIcon size={20} variant="subtle" color={copied ? "green" : "gray"} onClick={copy} aria-label={t("chat.copy_email")}>
                              {copied ? <IconCheck size={12} /> : <IconCopy size={12} />}
                            </ActionIcon>
                          </Tooltip>
                        )}
                      </CopyButton>
                    </Group>
                  ) : (
                    <Text size="sm" c="dimmed">
                      —
                    </Text>
                  )}
                </InspectorField>
                <InspectorField label={t("contacts.profile")}>
                  <Link
                    to="/$slug/contacts/$id"
                    params={{ slug, id: chat.contact.id }}
                    search={{ sort: "recent" }}
                    className="inbox-contact-link"
                    style={{ display: "inline-flex", alignItems: "center", gap: 4 }}
                  >
                    <Text span size="sm" c="brand" fw={500}>
                      {t("chat.open_contact")}
                    </Text>
                    <IconExternalLink size={14} />
                  </Link>
                </InspectorField>
              </>
            )}
          </InspectorSection>

          <InspectorSection
            title={t("chat.business_colleagues")}
            collapsible
            storeKey="chat-business-colleagues"
            extra={
              <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                {memberParticipants.length}
              </Text>
            }
          >
            <Stack gap="sm">
              <Text size="xs" c="dimmed">
                {t("chat.business_colleagues_hint")}
              </Text>

              {addable.length > 0 && (
                <Stack gap="xs">
                  <MultiSelect
                    placeholder={t("chat.pick_member_placeholder")}
                    value={toAdd}
                    onChange={setToAdd}
                    data={addable}
                    searchable
                    clearable
                  />
                  {toAdd.length > 0 && (
                    <Group justify="flex-end">
                      <Button size="compact-xs" leftSection={<IconUserPlus size={14} />} loading={addMembers.isPending} onClick={() => addMembers.mutate()}>
                        {t("chat.invite_colleagues_submit")}
                      </Button>
                    </Group>
                  )}
                </Stack>
              )}

              <Stack gap={8}>
                {memberParticipants.map((p) => {
                  const isMe = p.id === me.data?.id
                  return (
                    <Group key={p.id} gap="sm" wrap="nowrap">
                      <Indicator size={8} offset={2} position="bottom-end" color={p.status === "online" ? "green" : "gray"} withBorder>
                        <Avatar size={28} radius="xl" name={p.name || t("chat.unnamed_member")} color="initials" src={p.avatar_url || undefined} />
                      </Indicator>
                      <Group gap={6} wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
                        <Text size="sm" truncate>
                          {p.name || t("chat.unnamed_member")}
                        </Text>
                        {isMe && (
                          <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                            {t("inbox.you")}
                          </Badge>
                        )}
                      </Group>
                      {!isMe && memberParticipants.length > 1 && (
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

              {canLeave && (
                <>
                  <Divider />
                  <Button variant="light" color="red" size="xs" leftSection={<IconLogout size={14} />} loading={leave.isPending} onClick={() => void handleLeave()}>
                    {t("chat.leave")}
                  </Button>
                </>
              )}
            </Stack>
          </InspectorSection>

          {chat.mode === "group" && contactParticipants.length > 0 && (
            <InspectorSection
              title={t("chat.business_external_members")}
              collapsible
              storeKey="chat-business-external"
              extra={
                <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                  {contactParticipants.length}
                </Text>
              }
            >
              <Stack gap={8}>
                {contactParticipants.map((p) => (
                  <Group key={p.id} gap="sm" wrap="nowrap">
                    <Avatar size={28} radius="xl" name={p.name || t("inbox.visitor")} color="initials" />
                    <Link
                      to="/$slug/contacts/$id"
                      params={{ slug, id: p.id }}
                      search={{ sort: "recent" }}
                      className="inbox-contact-link"
                      style={{ flex: 1, minWidth: 0 }}
                    >
                      <Text size="sm" c="brand" fw={500} truncate>
                        {p.name || t("inbox.visitor")}
                      </Text>
                    </Link>
                  </Group>
                ))}
              </Stack>
            </InspectorSection>
          )}
        </Inspector>
      </PaneBody>
    </Stack>
  )
}
