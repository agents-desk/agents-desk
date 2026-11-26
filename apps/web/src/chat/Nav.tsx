import type { ChatScope } from "@agents-desk/client/tenant"
import { NavLink, Text } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { useNavigate, useParams } from "@tanstack/react-router"
import { IconBriefcase, IconMessageCircle, IconMessages, IconPlus, IconUsersGroup } from "@tabler/icons-react"
import { Fill, RailSection, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import { NavCount } from "../components/nav-count"
import { StatusMark } from "../components/status-mark"
import { ChannelIcon } from "../inbox/channel-badge"
import type { ChatModeFilter } from "./data"

/** ChatNavTarget 是二级导航栏切换后的目标筛选条件。 */
export interface ChatNavTarget {
  scope: ChatScope
  filter?: Exclude<ChatModeFilter, "all">
  channel?: string
}

/**
 * ChatNav 是放进二级导航（nav 插槽）的聊天分组菜单：
 * 采用对称的双分组结构——「内部聊天（全部/单聊/群聊）」与「商务沟通（全部商务沟通与个人商务渠道）」，
 * 两组均支持折叠并持久记住状态，与收件箱、联系人、报表保持一致的 200px 二级导航栏布局。
 */
export function ChatNav({
  scope,
  filter,
  channelId,
  onSelect,
}: {
  scope: ChatScope
  filter: ChatModeFilter
  channelId?: string
  onSelect: (next: ChatNavTarget) => void
}): ReactNode {
  const t = useT()
  const { slug } = useParams({ from: "/t/$slug" })
  const navigate = useNavigate()
  const unread = useQuery(kit.queries.chat.unread())
  const businessLinks = useQuery(kit.queries.business.linkList())
  const tgAccounts = useQuery(kit.queries.tgaccount.list())

  const internalUnread = unread.data?.internal_unread_messages ?? 0
  const externalUnread = unread.data?.external_unread_messages ?? 0
  const links = businessLinks.data ?? []
  const accounts = tgAccounts.data?.accounts ?? []
  const hasPersonalChannels = links.length > 0 || accounts.length > 0

  const goBusinessSettings = () => {
    void navigate({ to: "/t/$slug/settings/business-channels", params: { slug } })
  }

  return (
    <Fill name="nav">
      <RailSection title={t("chat.internal_section")} collapsible storeKey="chat-internal">
        <NavLink
          label={t("chat.filter_all")}
          leftSection={<IconMessages size={16} stroke={1.6} />}
          rightSection={
            <NavCount n={internalUnread} unread />
          }
          active={scope === "internal" && filter === "all"}
          onClick={() => onSelect({ scope: "internal" })}
        />
        <NavLink
          label={t("chat.filter_direct")}
          leftSection={<IconMessageCircle size={16} stroke={1.6} />}
          active={scope === "internal" && filter === "direct"}
          onClick={() => onSelect({ scope: "internal", filter: "direct" })}
        />
        <NavLink
          label={t("chat.filter_group")}
          leftSection={<IconUsersGroup size={16} stroke={1.6} />}
          active={scope === "internal" && filter === "group"}
          onClick={() => onSelect({ scope: "internal", filter: "group" })}
        />
      </RailSection>

      <RailSection title={t("chat.business_section")} collapsible storeKey="chat-business">
        <NavLink
          label={t("chat.business_all")}
          leftSection={<IconBriefcase size={16} stroke={1.6} />}
          rightSection={
            <NavCount n={externalUnread} unread />
          }
          active={scope === "external" && !channelId}
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
            active={scope === "external" && channelId === ch.id}
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
              active={scope === "external" && channelId === acc.id}
              onClick={() => onSelect({ scope: "external", channel: acc.id })}
            />
          )
        })}

        {!businessLinks.isPending && !tgAccounts.isPending && !hasPersonalChannels && (
          <NavLink
            label={
              <Text size="xs" c="dimmed">
                {t("chat.business_setup_cta")}
              </Text>
            }
            leftSection={<IconPlus size={14} stroke={1.6} />}
            onClick={goBusinessSettings}
          />
        )}
      </RailSection>

      {unread.data && unread.data.unread_chats > 0 && (
        <Text size="xs" c="dimmed" px={12} pt={8}>
          {t("chat.unread_chats_hint", { n: unread.data.unread_chats })}
        </Text>
      )}
    </Fill>
  )
}
