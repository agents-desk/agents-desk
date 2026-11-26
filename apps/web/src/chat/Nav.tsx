import type { ChatScope } from "@agents-desk/client/tenant"
import { ActionIcon, Badge, Box, NavLink, Text, Tooltip } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { useNavigate, useParams } from "@tanstack/react-router"
import { IconBriefcase, IconMessageCircle, IconMessages, IconPlus, IconSettings, IconUsersGroup } from "@tabler/icons-react"
import { Fill, RailSection, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
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
 * 上半区提供内部聊天分组（全部、单聊、群聊）；
 * 下半区提供商务沟通（全部商务沟通与成员个人网页链接、Telegram 个人号快捷筛选），
 * 与收件箱、联系人、报表保持一致的 200px 二级导航栏布局。
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
      <NavLink
        label={t("chat.internal_section")}
        leftSection={<IconMessages size={16} stroke={1.6} />}
        rightSection={
          internalUnread > 0 ? (
            <Badge size="xs" circle color="brand" style={{ fontVariantNumeric: "tabular-nums" }}>
              {internalUnread > 99 ? "99+" : internalUnread}
            </Badge>
          ) : undefined
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

      <RailSection
        title={t("chat.business_section")}
        collapsible
        storeKey="chat-business"
        action={
          <Tooltip label={t("chat.business_manage")} openDelay={300}>
            <ActionIcon size="xs" variant="subtle" color="gray" aria-label={t("chat.business_manage")} onClick={goBusinessSettings}>
              <IconSettings size={13} />
            </ActionIcon>
          </Tooltip>
        }
      >
        <NavLink
          label={t("chat.business_all")}
          leftSection={<IconBriefcase size={16} stroke={1.6} />}
          rightSection={
            externalUnread > 0 ? (
              <Badge size="xs" circle color="brand" style={{ fontVariantNumeric: "tabular-nums" }}>
                {externalUnread > 99 ? "99+" : externalUnread}
              </Badge>
            ) : undefined
          }
          active={scope === "external" && !channelId}
          onClick={() => onSelect({ scope: "external" })}
        />

        {links.map((ch) => (
          <NavLink
            key={ch.id}
            label={ch.name}
            leftSection={<ChannelIcon kind="business_link" size={15} />}
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
          const dotColor = !acc.enabled
            ? "var(--mantine-color-gray-5)"
            : acc.status === "connected"
              ? "var(--mantine-color-green-6)"
              : acc.status === "auth_expired"
                ? "var(--mantine-color-red-6)"
                : "var(--mantine-color-gray-5)"
          return (
            <NavLink
              key={acc.id}
              label={acc.name}
              leftSection={<ChannelIcon kind="telegram_account" size={15} />}
              rightSection={
                <Box
                  w={7}
                  h={7}
                  style={{
                    borderRadius: "50%",
                    backgroundColor: dotColor,
                    flex: "none",
                  }}
                />
              }
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
