import { Badge, NavLink, Text } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { IconMessageCircle, IconMessages, IconUsersGroup } from "@tabler/icons-react"
import { Fill, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import type { ChatModeFilter } from "./data"

/**
 * ChatNav 是放进二级导航（nav 插槽）的聊天分组菜单：
 * 提供全部、单聊与群聊筛选，与收件箱、联系人、报表保持一致的 200px 二级导航栏布局。
 */
export function ChatNav({
  filter,
  onSelect,
}: {
  filter: ChatModeFilter
  onSelect: (next: ChatModeFilter) => void
}): ReactNode {
  const t = useT()
  const unread = useQuery(kit.queries.chat.unread())
  const unreadCount = unread.data?.unread_messages ?? 0

  return (
    <Fill name="nav">
      <NavLink
        label={t("chat.filter_all")}
        leftSection={<IconMessages size={16} stroke={1.6} />}
        rightSection={
          unreadCount > 0 ? (
            <Badge size="xs" circle color="brand" style={{ fontVariantNumeric: "tabular-nums" }}>
              {unreadCount > 99 ? "99+" : unreadCount}
            </Badge>
          ) : undefined
        }
        active={filter === "all"}
        onClick={() => onSelect("all")}
      />
      <NavLink
        label={t("chat.filter_direct")}
        leftSection={<IconMessageCircle size={16} stroke={1.6} />}
        active={filter === "direct"}
        onClick={() => onSelect("direct")}
      />
      <NavLink
        label={t("chat.filter_group")}
        leftSection={<IconUsersGroup size={16} stroke={1.6} />}
        active={filter === "group"}
        onClick={() => onSelect("group")}
      />
      {unread.data && unread.data.unread_chats > 0 && (
        <Text size="xs" c="dimmed" px={12} pt={8}>
          {t("chat.unread_chats_hint", { n: unread.data.unread_chats })}
        </Text>
      )}
    </Fill>
  )
}
