import type { ChatScope } from "@agents-desk/client/tenant"
import { ActionIcon, NavLink, Text, Tooltip } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { useNavigate, useParams } from "@tanstack/react-router"
import { IconBriefcase, IconMessageCircle, IconMessages, IconPlus, IconUsersGroup } from "@tabler/icons-react"
import { RailSection, useCan, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import { NavCount } from "../components/nav-count"
import { StatusMark } from "../components/status-mark"
import { ChannelIcon } from "../inbox/channel-badge"
import type { ChatModeFilter } from "./data"

/** ChatNavTarget 是二级导航中一个聊天分组的筛选条件：内部聊天（全部、单聊、群聊）或商务沟通（全部、某个个人商务渠道）。 */
export interface ChatNavTarget {
  scope: ChatScope
  filter?: Exclude<ChatModeFilter, "all">
  channel?: string
}

/**
 * ChatSections 是收件箱二级导航中的两个聊天分组：内部聊天（全部、单聊、群聊）和商务沟通（全部、自己的每个个人商务渠道），
 * 分组都可以折叠，折叠状态按成员记住。active 是当前打开的聊天分组，不在聊天页时为空；onNewChat 打开新建聊天的弹窗。
 */
export function ChatSections({
  active,
  onSelect,
  onNewChat,
}: {
  active?: ChatNavTarget
  onSelect: (next: ChatNavTarget) => void
  onNewChat: () => void
}): ReactNode {
  const t = useT()
  const can = useCan()
  const canUseBusiness = can("business-channels:use")
  const { slug } = useParams({ from: "/$slug" })
  const navigate = useNavigate()
  const unread = useQuery(kit.queries.chat.unread())
  const businessLinks = useQuery({ ...kit.queries.business.linkList(), enabled: canUseBusiness })
  const tgAccounts = useQuery({ ...kit.queries.tgaccount.list(), enabled: canUseBusiness })

  const internalUnread = unread.data?.internal_unread_messages ?? 0
  const externalUnread = unread.data?.external_unread_messages ?? 0
  const links = businessLinks.data ?? []
  const accounts = tgAccounts.data?.accounts ?? []
  const hasPersonalChannels = links.length > 0 || accounts.length > 0
  const internal = active?.scope === "internal"
  const external = active?.scope === "external"

  return (
    <>
      <RailSection
        title={t("chat.internal_section")}
        collapsible
        storeKey="chat-internal"
        action={
          <Tooltip label={t("chat.new")} openDelay={300}>
            <ActionIcon size="xs" variant="subtle" color="gray" aria-label={t("chat.new")} onClick={onNewChat}>
              <IconPlus size={14} stroke={1.6} />
            </ActionIcon>
          </Tooltip>
        }
      >
        <NavLink
          label={t("chat.filter_all")}
          leftSection={<IconMessages size={16} stroke={1.6} />}
          rightSection={<NavCount n={internalUnread} unread />}
          active={internal && !active?.filter}
          onClick={() => onSelect({ scope: "internal" })}
        />
        <NavLink
          label={t("chat.filter_direct")}
          leftSection={<IconMessageCircle size={16} stroke={1.6} />}
          active={internal && active?.filter === "direct"}
          onClick={() => onSelect({ scope: "internal", filter: "direct" })}
        />
        <NavLink
          label={t("chat.filter_group")}
          leftSection={<IconUsersGroup size={16} stroke={1.6} />}
          active={internal && active?.filter === "group"}
          onClick={() => onSelect({ scope: "internal", filter: "group" })}
        />
      </RailSection>

      <RailSection title={t("chat.business_section")} collapsible storeKey="chat-business">
        <NavLink
          label={t("chat.business_all")}
          leftSection={<IconBriefcase size={16} stroke={1.6} />}
          rightSection={<NavCount n={externalUnread} unread />}
          active={external && !active?.channel}
          onClick={() => onSelect({ scope: "external" })}
        />

        {links.map((ch) => (
          <NavLink
            key={ch.id}
            label={ch.name}
            leftSection={<ChannelIcon kind="business_link" size={16} />}
            rightSection={
              !ch.enabled ? (
                <Text size="xs" c="dimmed">
                  {t("business_channels.disabled_short")}
                </Text>
              ) : undefined
            }
            active={external && active?.channel === ch.id}
            onClick={() => onSelect({ scope: "external", channel: ch.id })}
          />
        ))}

        {accounts.map((acc) => {
          const dotColor = !acc.enabled ? "gray" : acc.status === "connected" ? "green" : acc.status === "auth_expired" ? "red" : "gray"
          return (
            <NavLink
              key={acc.id}
              label={acc.name}
              leftSection={<ChannelIcon kind="telegram_account" size={16} />}
              rightSection={<StatusMark color={dotColor} />}
              active={external && active?.channel === acc.id}
              onClick={() => onSelect({ scope: "external", channel: acc.id })}
            />
          )
        })}

        {canUseBusiness && !businessLinks.isPending && !tgAccounts.isPending && !hasPersonalChannels && (
          <NavLink
            label={
              <Text size="xs" c="dimmed">
                {t("chat.business_setup_cta")}
              </Text>
            }
            leftSection={<IconPlus size={14} stroke={1.6} />}
            onClick={() => void navigate({ to: "/$slug/settings/business-channels", params: { slug } })}
          />
        )}
      </RailSection>
    </>
  )
}
