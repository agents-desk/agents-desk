import type { ConversationChatDTO } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Avatar,
  Badge,
  CopyButton,
  Group,
  Indicator,
  Loader,
  Stack,
  Text,
  ThemeIcon,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { IconCheck, IconCopy, IconUsersGroup, IconX } from "@tabler/icons-react"
import { Inspector, InspectorField, InspectorSection, PaneBody, PaneHeader, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../api"
import { AiAssistantPanel, type DetailsAsideTab, DetailsAsideTabs, useAiWorkspaceEnabled } from "../components/ai-placeholders"
import { useMembers } from "../inbox/data"
import { languageName } from "../languages"

/** PeerDetailsProps 是单聊右侧同事详情栏的设置。 */
interface PeerDetailsProps {
  chat: ConversationChatDTO
  onClose: () => void
  onOpenChat: (chatId: string) => void
}

/**
 * PeerDetails 是内部单聊的右侧详情栏：
 * 展示对方头像、在线状态、角色、邮箱、偏好语言，以及双方共同加入的内部群聊并支持点击快速跳转。
 */
export function PeerDetails({ chat, onClose, onOpenChat }: PeerDetailsProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const { byId } = useMembers()
  const [asideTab, setAsideTab] = useState<DetailsAsideTab>("details")

  const peerId = chat.peer?.id ?? ""
  const member = peerId ? byId.get(peerId) : undefined
  const name = member?.name ?? chat.peer?.name ?? t("chat.unknown_peer")
  const avatarUrl = member?.avatar_url || chat.peer?.avatar_url || undefined
  const online = (member?.status ?? chat.peer?.status) === "online"
  const lang = member?.language || chat.peer?.language

  const sharedGroups = useQuery({
    queryKey: [...kit.queries.chat.key(), "shared-groups", peerId],
    queryFn: () => tenantApi.chat.list({ mode: "group", member_id: peerId, per_page: 50 }),
    enabled: Boolean(peerId),
  })
  const groups = sharedGroups.data?.items ?? []
  const aiEnabled = useAiWorkspaceEnabled()

  return (
    <Stack gap={0} h="100%" className="inbox-details">
      <PaneHeader
        title={t("chat.peer_details")}
        actions={
          <ActionIcon variant="subtle" color="gray" size="sm" aria-label={t("inbox.close_details")} onClick={onClose}>
            <IconX size={16} />
          </ActionIcon>
        }
      />
      <DetailsAsideTabs value={asideTab} onChange={setAsideTab} />

      <PaneBody>
        {aiEnabled && asideTab === "ai" ? (
          <AiAssistantPanel scene="chat" />
        ) : (
        <>
        <Stack gap="sm" px="var(--tn-pane-px)" pt="md" pb="sm" align="center">
          <Indicator size={11} offset={4} position="bottom-end" color={online ? "green" : "gray"} withBorder>
            <Avatar size={56} radius="xl" name={name} color="initials" src={avatarUrl} />
          </Indicator>
          <Stack gap={4} align="center" w="100%">
            <Text size="md" fw={600} ta="center" truncate maw="100%">
              {name}
            </Text>
            <Group gap={6} justify="center">
              {member?.role_name && (
                <Badge size="xs" variant="light" color="gray">
                  {member.role_name}
                </Badge>
              )}
              <Badge size="xs" variant="dot" color={online ? "green" : "gray"}>
                {t(online ? "chat.status_online" : "chat.status_away")}
              </Badge>
            </Group>
          </Stack>
        </Stack>

        <Inspector>
          <InspectorSection title={t("chat.peer_info")} collapsible storeKey="chat-peer-info">
            <InspectorField label={t("inbox.contact_email")}>
              {member?.email ? (
                <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
                  <Text size="sm" truncate style={{ flex: 1, minWidth: 0 }}>
                    {member.email}
                  </Text>
                  <CopyButton value={member.email} timeout={1500}>
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
            <InspectorField label={t("chat.peer_language")}>
              <Text size="sm">{lang ? languageName(lang, ui) : "—"}</Text>
            </InspectorField>
            <InspectorField label={t("chat.peer_role")}>
              <Text size="sm">{member?.role_name || "—"}</Text>
            </InspectorField>
          </InspectorSection>

          <InspectorSection
            title={t("chat.shared_groups")}
            collapsible
            storeKey="chat-peer-groups"
            extra={
              groups.length > 0 ? (
                <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                  {groups.length}
                </Text>
              ) : undefined
            }
          >
            {sharedGroups.isPending ? (
              <Group justify="center" py="xs">
                <Loader size="xs" />
              </Group>
            ) : groups.length === 0 ? (
              <Text size="xs" c="dimmed">
                {t("chat.shared_groups_empty")}
              </Text>
            ) : (
              <Stack gap={4}>
                {groups.map((g) => (
                  <UnstyledButton
                    key={g.id}
                    className="inbox-history"
                    px={6}
                    py={6}
                    onClick={() => onOpenChat(g.id)}
                  >
                    <Group gap="xs" wrap="nowrap">
                      <ThemeIcon size={24} radius="xl" variant="light" color="brand" style={{ flex: "none" }}>
                        <IconUsersGroup size={14} stroke={1.6} />
                      </ThemeIcon>
                      <Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
                        <Text size="sm" truncate>
                          {g.name || t("chat.untitled_group")}
                        </Text>
                        <Text size="xs" c="dimmed">
                          {t("chat.members_count", { n: g.participant_count })}
                        </Text>
                      </Stack>
                    </Group>
                  </UnstyledButton>
                ))}
              </Stack>
            )}
          </InspectorSection>
        </Inspector>
        </>
        )}
      </PaneBody>
    </Stack>
  )
}
