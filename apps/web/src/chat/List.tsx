import type { ChatScope, ConversationChatDTO } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Avatar,
  Badge,
  Box,
  Button,
  Center,
  CloseButton,
  Group,
  Indicator,
  Loader,
  Skeleton,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useQuery } from "@tanstack/react-query"
import { IconPlus, IconSearch, IconUsersGroup } from "@tabler/icons-react"
import { PaneBody, PaneHeader, PaneToolbar, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react"
import { kit } from "../api"
import { previewSummary } from "../inbox/Attachments"
import { channelColor, ChannelIcon } from "../inbox/channel-badge"
import { useMe, useMembers } from "../inbox/data"
import { type FillWatcher, useFillVisible } from "../inbox/fill"
import { activity } from "../inbox/format"
import { HighlightedSnippet } from "../inbox/Highlight"
import { chatDisplayName, type ChatModeFilter, flattenChats, useChatList } from "./data"
import { EmptyState } from "../components/empty-state"
import { useListKeys } from "../layout"

/** ChatListProps 是聊天列表栏的设置。 */
interface ChatListProps {
  scope: ChatScope
  mode: ChatModeFilter
  channelId?: string
  channelName?: string
  openId?: string
  open: (id: string) => void
  onNewChat: () => void
  onManageBusinessChannels: () => void
}

/**
 * ChatList 是“聊天”模块的会话列表栏：
 * 顶栏显示当前分组名称（内部聊天或商务沟通渠道）及操作按钮，工具条支持按同事名、联系人名、群名或消息全文检索，
 * 列表按最近消息倒序展示头像/在线状态或商务渠道标识、未读数角标、跨语言译文预览与相对时间。
 */
export function ChatList({
  scope,
  mode,
  channelId,
  channelName,
  openId,
  open,
  onNewChat,
  onManageBusinessChannels,
}: ChatListProps): ReactNode {
  const t = useT()
  const me = useMe()
  const translation = useQuery(kit.queries.translation.settings())
  const watch = useFillVisible(translation.data?.enabled ?? false, me.data?.language)

  const [q, setQ] = useState("")
  const [debouncedQ] = useDebouncedValue(q, 200)
  const list = useChatList(debouncedQ, mode, scope, channelId)
  const items = flattenChats(list.data?.pages)
  // 列表的顺序按 ID 串比较，数据刷新但顺序不变时不重新登记快捷键
  const idsKey = items.map((x) => x.id).join("\n")
  const ids = useMemo(() => (idsKey ? idsKey.split("\n") : []), [idsKey])
  useListKeys(ids, openId, open)

  // 滚到底时加载下一页
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = end.current
    if (!el) return
    const io = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting && list.hasNextPage && !list.isFetchingNextPage) {
        void list.fetchNextPage()
      }
    })
    io.observe(el)
    return () => io.disconnect()
  }, [list])

  const isExternal = scope === "external"
  const hasFilters = Boolean(debouncedQ.trim() || (!isExternal && mode !== "all") || (isExternal && channelId))
  const heading = isExternal
    ? channelName || t("chat.business_all")
    : mode === "direct"
      ? t("chat.filter_direct")
      : mode === "group"
        ? t("chat.filter_group")
        : t("chat.internal_section")

  return (
    <Stack gap={0} h="100%">
      <PaneHeader
        showNavToggle
        title={
          <Text fw={600} size="sm" truncate lh={1.25}>
            {heading}
          </Text>
        }
        actions={
          isExternal ? (
            <Button size="compact-xs" variant="subtle" color="gray" onClick={onManageBusinessChannels}>
              {t("chat.business_manage")}
            </Button>
          ) : (
            <Tooltip label={t("chat.new")} openDelay={300}>
              <ActionIcon variant="light" size="sm" aria-label={t("chat.new")} onClick={onNewChat}>
                <IconPlus size={16} />
              </ActionIcon>
            </Tooltip>
          )
        }
      />

      <PaneToolbar gap={6}>
        <TextInput
          variant="filled"
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape" && q) {
              e.stopPropagation()
              setQ("")
            }
          }}
          placeholder={t(isExternal ? "chat.business_search_placeholder" : "chat.search_placeholder")}
          aria-label={t(isExternal ? "chat.business_search_placeholder" : "chat.search_placeholder")}
          leftSection={<IconSearch size={14} stroke={1.6} />}
          rightSection={q ? <CloseButton size="xs" aria-label={t("common.clear_search")} onClick={() => setQ("")} /> : undefined}
        />
      </PaneToolbar>

      <PaneBody>
        {list.isPending ? (
          <Stack gap={0}>
            {[0, 1, 2, 3, 4].map((i) => (
              <Box key={i} px="sm" py={10} className="inbox-row">
                <Group gap="sm" wrap="nowrap">
                  <Skeleton circle h={34} w={34} />
                  <Stack gap={6} style={{ flex: 1 }}>
                    <Skeleton h={12} w="54%" radius="sm" />
                    <Skeleton h={10} w="78%" radius="sm" />
                  </Stack>
                </Group>
              </Box>
            ))}
          </Stack>
        ) : items.length === 0 ? (
          <EmptyState
            title={t(hasFilters ? "chat.empty_filtered" : isExternal ? "chat.business_empty" : "chat.empty")}
            action={
              isExternal &&
              !debouncedQ.trim() && (
                <Button size="xs" variant="light" onClick={onManageBusinessChannels}>
                  {t("chat.business_manage")}
                </Button>
              )
            }
          />
        ) : (
          items.map((chat) => <ChatRow key={chat.id} chat={chat} active={chat.id === openId} watch={watch} onClick={() => open(chat.id)} />)
        )}
        <div ref={end} />
        {list.isFetchingNextPage && (
          <Center p="sm">
            <Loader size="xs" />
          </Center>
        )}
      </PaneBody>
    </Stack>
  )
}

/** ChatRow 是聊天列表中的单行项：单聊显示对方头像（内部聊天带在线圆点，商务沟通带渠道图标），群聊显示群图标，右侧展示相对时间与未读数角标。 */
function ChatRow({
  chat,
  active,
  watch,
  onClick,
}: {
  chat: ConversationChatDTO
  active: boolean
  watch: FillWatcher
  onClick: () => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const me = useMe()
  const { byId } = useMembers()

  const isExternal = chat.scope === "external"
  const title = chatDisplayName(chat, t)
  const rawPreview = previewSummary(chat.preview, chat.preview_attachment, t)

  let senderPrefix = ""
  if (chat.mode === "group" && chat.preview_actor && rawPreview) {
    if (chat.preview_actor.type === "member") {
      const name = chat.preview_actor.id === me.data?.id ? t("inbox.you") : (byId.get(chat.preview_actor.id)?.name ?? "")
      if (name) senderPrefix = `${name}: `
    } else if (chat.preview_actor.type === "contact") {
      const name = chat.preview_actor.name || chat.contact?.name || t("inbox.visitor")
      if (name) senderPrefix = `${name}: `
    }
  }
  const previewText = rawPreview ? `${senderPrefix}${rawPreview}` : t("chat.no_messages")
  const matchSnippet = chat.match?.snippet
  const matchCount = chat.match_count ?? 0
  const chColor = chat.channel ? channelColor(chat.channel.kind) : "brand"

  return (
    <UnstyledButton
      ref={chat.preview_untranslated && chat.preview_item_id ? watch(chat.preview_item_id) : undefined}
      onClick={onClick}
      className="inbox-row"
      data-active={active || undefined}
      w="100%"
      px="var(--tn-pane-px)"
      py={10}
    >
      <Group gap="sm" wrap="nowrap" align="center" w="100%" style={{ minWidth: 0 }}>
        {chat.mode === "group" ? (
          <ThemeIcon size={36} radius="xl" variant="light" color={isExternal ? chColor : "brand"} style={{ flex: "none" }}>
            <IconUsersGroup size={18} stroke={1.6} />
          </ThemeIcon>
        ) : isExternal ? (
          <Avatar size={36} radius="xl" name={title} color="initials" style={{ flex: "none" }} />
        ) : (
          <Indicator
            size={9}
            offset={3}
            position="bottom-end"
            color={chat.peer?.status === "online" ? "green" : "gray"}
            withBorder
            style={{ flex: "none" }}
          >
            <Avatar size={36} radius="xl" name={title} color="initials" src={chat.peer?.avatar_url || undefined} />
          </Indicator>
        )}

        <Stack gap={3} style={{ flex: 1, minWidth: 0 }}>
          <Group gap={5} wrap="nowrap" style={{ minWidth: 0 }}>
            {isExternal && chat.channel && (
              <Tooltip label={chat.channel.name} openDelay={300}>
                <Box
                  component="span"
                  c={chColor}
                  style={{ display: "inline-flex", alignItems: "center", flex: "none" }}
                >
                  <ChannelIcon kind={chat.channel.kind} size={13} />
                </Box>
              </Tooltip>
            )}
            <Text size="sm" fw={chat.unread ? 700 : 600} truncate style={{ flex: 1, minWidth: 0 }}>
              {title}
            </Text>
            <Text size="xs" c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
              {activity(chat.last_message_at, ui, t, zone)}
            </Text>
          </Group>

          <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
            {matchSnippet && matchSnippet.length > 0 ? (
              <Text size="xs" c="dimmed" truncate style={{ flex: 1, minWidth: 0 }}>
                <HighlightedSnippet parts={matchSnippet} />
              </Text>
            ) : (
              <Text size="xs" c={chat.unread ? undefined : "dimmed"} fw={chat.unread ? 500 : undefined} truncate style={{ flex: 1, minWidth: 0 }}>
                {previewText}
              </Text>
            )}
            {matchCount > 1 ? (
              <Badge size="xs" variant="light" color="gray" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                {t("inbox.search_match_count", { n: matchCount })}
              </Badge>
            ) : chat.unread_count > 0 ? (
              <Badge size="xs" circle color="brand" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                {chat.unread_count > 99 ? "99+" : chat.unread_count}
              </Badge>
            ) : null}
          </Group>
        </Stack>
      </Group>
    </UnstyledButton>
  )
}
