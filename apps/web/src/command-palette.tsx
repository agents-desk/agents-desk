import { Badge, Box, CloseButton, Group, Kbd, Loader, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { useDebouncedValue, useOs } from "@mantine/hooks"
import { Spotlight, spotlight } from "@mantine/spotlight"
import {
  IconArrowRight,
  IconAt,
  IconCheck,
  IconClock,
  IconFilter,
  IconFlag,
  IconInbox,
  IconKeyboard,
  IconListDetails,
  IconLock,
  IconMessage,
  IconMessageCircle,
  IconRefresh,
  IconSearch,
  IconSwitchHorizontal,
  IconTicket,
  IconUser,
  IconUserOff,
  IconUserPlus,
  IconUserQuestion,
  IconUsers,
  IconUsersGroup,
} from "@tabler/icons-react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useMatch, useNavigate } from "@tanstack/react-router"
import { Icon, type MenuItem, notifyError, notifySuccess, useCan, useLanguage, useNavigation, useT, useTenant, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useMemo, useState } from "react"
import { kit, tenantApi } from "./api"
import { chatDisplayName } from "./chat/data"
import { contactDisplayName, contactSubline } from "./contacts/display"
import { useMe, useMembers, useTeams } from "./inbox/data"
import { activity, conversationStatusColor } from "./inbox/format"
import { HighlightedSnippet, HighlightQuery } from "./inbox/Highlight"
import { snoozeOptions } from "./inbox/menus"
import type { MatchField } from "./inbox/search"
import { ticketStatusColor, ticketStatusLabel } from "./tickets/meta"
import { useWorkspaces } from "./workspace"

/** flattenPages 将菜单树拍平为叶子页面列表，保留上级分组名称和最近图标。 */
function flattenPages(
  items: readonly MenuItem[],
  trail: string[] = [],
  icon?: string,
): { item: MenuItem; trail: string[]; icon?: string }[] {
  return items.flatMap((item) =>
    item.children?.length
      ? flattenPages(item.children, [...trail, item.title], item.icon ?? icon)
      : [{ item, trail, icon: item.icon ?? icon }],
  )
}

/** firstLeaf 返回菜单分支下的第一个可访问页面。 */
function firstLeaf(item: MenuItem): MenuItem {
  return item.children?.length ? firstLeaf(item.children[0]) : item
}


/** CommandSearchButton 是放在图标栏顶部的全局搜索入口，点击或按 Cmd/Ctrl + K 打开命令面板。 */
export function CommandSearchButton(): ReactNode {
  const t = useT()
  const os = useOs()
  const shortcut = os === "macos" || os === "ios" ? "⌘K" : "Ctrl+K"
  return (
    <Tooltip label={`${t("command.search")}  ${shortcut}`} position="right" openDelay={350}>
      <UnstyledButton className="tn-rail-module" aria-label={t("command.search")} onClick={spotlight.open}>
        <IconSearch size={20} stroke={1.6} />
      </UnstyledButton>
    </Tooltip>
  )
}

interface NavAction {
  id: string
  label: string
  description?: string
  icon: ReactNode
  quick?: boolean // 是否在未输入关键词的默认面板中展示
  onClick: () => void
}

/**
 * CommandPalette 是工作台的全局命令面板（Cmd/Ctrl + K）：
 * 高度固定，输入筛选时尺寸保持稳定；处于会话页时顶部提供当前会话的指派、关闭、稍后处理与优先级快捷操作；
 * 未输入时展示精简的收件箱与一级模块快捷入口，输入关键词后实时全文搜索会话消息（含高亮摘要）、联系人、收件箱、全部页面与改派动作。
 */
export function CommandPalette({
  slug,
  impersonation,
  onShowShortcuts,
}: {
  slug: string
  impersonation: boolean
  onShowShortcuts: () => void
}): ReactNode {
  const t = useT()
  const can = useCan()
  const language = useLanguage()
  const zone = useTimeZone()
  const { menu } = useNavigation()
  const { switchTo } = useTenant()
  const { active: activeWorkspaces } = useWorkspaces()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [query, setQuery] = useState("")
  const trimmed = query.trim()
  const [debounced] = useDebouncedValue(trimmed, 250)
  const canReadInbox = can("conversations:read")
  const canReply = can("conversations:reply")
  const canAssignOthers = can("conversations:assign")
  const canReadContacts = can("contacts:read")
  const inbox = useQuery({ ...kit.queries.conversation.inbox(), enabled: canReadInbox })
  const convMatch = useMatch({ from: "/$slug/inbox/_conversations/$id", shouldThrow: false })
  const currentConvId = convMatch?.params.id
  const currentConv = useQuery({
    ...kit.queries.conversation.get(currentConvId ?? ""),
    enabled: Boolean(currentConvId && canReadInbox),
  })
  const me = useMe()
  const { list: members } = useMembers()
  const teams = useTeams()

  const convSearch = useQuery({
    queryKey: [...kit.queries.conversation.key(), "palette-search", debounced],
    queryFn: () => tenantApi.conversation.search({ q: debounced, per_page: 6 }),
    enabled: canReadInbox && debounced.length > 0,
  })
  const contactSearch = useQuery({
    queryKey: [...kit.queries.contact.key(), "palette-search", debounced],
    queryFn: () => tenantApi.contact.list({ q: debounced, per_page: 5 }),
    enabled: canReadContacts && debounced.length > 0,
  })
  const ticketNumberMatch = debounced.match(/^#?(\d+)$/)
  const exactTicketNum = ticketNumberMatch ? Number(ticketNumberMatch[1]) : 0
  const ticketExact = useQuery({
    queryKey: [...kit.queries.ticket.key(), "palette-number", exactTicketNum],
    queryFn: async () => {
      try {
        return await tenantApi.ticket.getByNumber(exactTicketNum)
      } catch {
        return null
      }
    },
    enabled: canReadInbox && exactTicketNum > 0,
    retry: false,
  })
  const ticketSearch = useQuery({
    queryKey: [...kit.queries.ticket.key(), "palette-search", debounced],
    queryFn: () => tenantApi.ticket.list({ q: debounced, per_page: 5 }),
    enabled: canReadInbox && debounced.length > 0,
  })
  const chatSearch = useQuery({
    queryKey: [...kit.queries.chat.key(), "palette-search", debounced],
    queryFn: () => tenantApi.chat.list({ q: debounced, per_page: 5 }),
    enabled: canReadInbox && debounced.length > 0,
  })

  const conversationActions = useMemo<NavAction[]>(() => {
    const c = currentConv.data
    if (!c || !canReply) return []
    const contactLabel = contactDisplayName(c.contact, t)
    const run = (fn: () => Promise<unknown>, okMessage: string) => {
      void fn()
        .then(() => {
          void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
          notifySuccess(okMessage)
        })
        .catch((err) => notifyError(err))
    }

    const actions: NavAction[] = []
    const assignedToMe = c.assignee?.type === "member" && c.assignee.id === me.data?.id
    if (me.data && !assignedToMe) {
      actions.push({
        id: "conv:assign:me",
        label: t("inbox.assign_me"),
        description: contactLabel,
        icon: <IconUserPlus size={16} stroke={1.6} />,
        quick: true,
        onClick: () => run(() => tenantApi.conversation.assign(c.id, { assignee_id: me.data.id }), t("command.action_assigned")),
      })
    }
    if (c.status === "closed") {
      actions.push({
        id: "conv:reopen",
        label: t("inbox.reopen"),
        description: contactLabel,
        icon: <IconRefresh size={16} stroke={1.6} />,
        quick: true,
        onClick: () => run(() => tenantApi.conversation.reopen(c.id), t("command.action_reopened")),
      })
    } else {
      actions.push({
        id: "conv:close",
        label: t("inbox.close"),
        description: contactLabel,
        icon: <IconCheck size={16} stroke={1.6} />,
        quick: true,
        onClick: () => run(() => tenantApi.conversation.close(c.id), t("command.action_closed")),
      })
      for (const opt of snoozeOptions(zone)) {
        const when = t(`inbox.snooze_${opt.key}` as const)
        actions.push({
          id: `conv:snooze:${opt.key}`,
          label: t("command.action_snooze", { when }),
          description: contactLabel,
          icon: <IconClock size={16} stroke={1.6} />,
          quick: opt.key === "later" || opt.key === "tomorrow",
          onClick: () => run(() => tenantApi.conversation.snooze(c.id, { until: opt.until.toISOString() }), t("command.action_snoozed")),
        })
      }
    }
    actions.push({
      id: "conv:priority",
      label: t(c.priority === "high" ? "command.action_priority_normal" : "command.action_priority_high"),
      description: contactLabel,
      icon: <IconFlag size={16} stroke={1.6} />,
      quick: false,
      onClick: () =>
        run(
          () => tenantApi.conversation.priority(c.id, { priority: c.priority === "high" ? "normal" : "high" }),
          t("command.action_priority_updated"),
        ),
    })

    if (canAssignOthers) {
      if (c.assignee) {
        actions.push({
          id: "conv:unassign",
          label: t("inbox.unassign"),
          description: contactLabel,
          icon: <IconUserOff size={16} stroke={1.6} />,
          quick: false,
          onClick: () => run(() => tenantApi.conversation.assign(c.id, { assignee_id: "" }), t("command.action_assigned")),
        })
      }
      for (const m of members) {
        if (m.id === me.data?.id || (c.assignee?.type === "member" && c.assignee.id === m.id)) continue
        actions.push({
          id: `conv:assign:member:${m.id}`,
          label: t("command.assign_member", { name: m.name }),
          description: m.status === "away" ? t("inbox.away") : contactLabel,
          icon: <IconUserPlus size={16} stroke={1.6} />,
          quick: false,
          onClick: () => run(() => tenantApi.conversation.assign(c.id, { assignee_id: m.id }), t("command.action_assigned")),
        })
      }
      for (const team of teams) {
        if (team.status !== "active" || c.team_id === team.id) continue
        actions.push({
          id: `conv:assign:team:${team.id}`,
          label: t("command.assign_team", { name: team.name }),
          description: contactLabel,
          icon: <IconUsersGroup size={16} stroke={1.6} />,
          quick: false,
          onClick: () => run(() => tenantApi.conversation.assign(c.id, { team_id: team.id }), t("command.action_assigned")),
        })
      }
    }
    return actions
  }, [canAssignOthers, canReply, currentConv.data, me.data, members, queryClient, t, teams, zone])

  const { inboxActions, quickPageActions, allPageActions } = useMemo(() => {
    const inboxList: NavAction[] = []
    if (canReadInbox) {
      const boxSearch = { status: "open" as const, sort: "waiting" as const }
      inboxList.push(
        {
          id: "inbox:mine",
          label: t("inbox.box_mine"),
          description: t("nav.inbox"),
          icon: <IconInbox size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "mine" } }),
        },
        {
          id: "inbox:mentions",
          label: t("inbox.box_mentions"),
          description: t("nav.inbox"),
          icon: <IconAt size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "mentions" } }),
        },
        {
          id: "inbox:unassigned",
          label: t("inbox.box_unassigned"),
          description: t("nav.inbox"),
          icon: <IconUserQuestion size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "unassigned" } }),
        },
        {
          id: "inbox:all",
          label: t("inbox.box_all"),
          description: t("nav.inbox"),
          icon: <IconListDetails size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "all" } }),
        },
        {
          id: "tickets:mine",
          label: t("tickets.command_mine"),
          description: t("tickets.nav_section"),
          icon: <IconTicket size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/$slug/inbox/tickets", params: { slug }, search: { box: "mine", status: "unresolved" } }),
        },
        {
          id: "tickets:unassigned",
          label: t("tickets.command_unassigned"),
          description: t("tickets.nav_section"),
          icon: <IconTicket size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/$slug/inbox/tickets", params: { slug }, search: { box: "unassigned", status: "unresolved" } }),
        },
        {
          id: "tickets:all",
          label: t("tickets.command_all"),
          description: t("tickets.nav_section"),
          icon: <IconTicket size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/$slug/inbox/tickets", params: { slug }, search: { box: "all", status: "unresolved" } }),
        },
        ...(inbox.data?.teams ?? []).map((team) => ({
          id: `inbox:team:${team.id}`,
          label: team.name,
          description: t("inbox.teams"),
          icon: <IconUsersGroup size={16} stroke={1.6} />,
          onClick: () =>
            void navigate({ to: "/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "all", team: team.id } }),
        })),
        ...(inbox.data?.views ?? []).map((v) => ({
          id: `inbox:view:${v.id}`,
          label: v.name,
          description: t("views.section"),
          icon: v.scope === "shared" ? <IconUsers size={16} stroke={1.6} /> : <IconFilter size={16} stroke={1.6} />,
          onClick: () =>
            void navigate({
              to: "/$slug/inbox",
              params: { slug },
              search: { ...boxSearch, box: "all", view: v.id, sort: v.config.sort ?? "waiting" },
            }),
        })),
      )
    }

    const shortcutsItem: NavAction = {
      id: "global:shortcuts",
      label: t("command.shortcuts_action"),
      description: "?",
      icon: <IconKeyboard size={16} stroke={1.6} />,
      onClick: onShowShortcuts,
    }

    // 初始空查询只放一级模块（排除已在上方展开的收件箱）和快捷键帮助入口，保持默认面板精简克制
    const inboxPath = `/${slug}/inbox`
    const quickPages: NavAction[] = [
      ...menu
        .filter((item) => !(canReadInbox && item.path === inboxPath))
        .map((item) => {
          const target = firstLeaf(item)
          return {
            id: `quick:${item.id}`,
            label: item.title,
            icon: <Icon name={item.icon ?? "apps"} size={15} stroke={1.6} />,
            onClick: () => void navigate({ to: target.path }),
          }
        }),
      shortcutsItem,
    ]

    // 输入关键词后搜索全部叶子页面（含所有设置子页）及快捷键帮助
    const allPages: NavAction[] = [
      ...flattenPages(menu).map(({ item, trail, icon }) => ({
        id: `page:${item.id}`,
        label: item.title,
        description: trail.length > 0 ? trail.join(" / ") : undefined,
        icon: <Icon name={icon ?? "apps"} size={15} stroke={1.6} />,
        onClick: () => void navigate({ to: item.path }),
      })),
      shortcutsItem,
    ]

    return { inboxActions: inboxList, quickPageActions: quickPages, allPageActions: allPages }
  }, [canReadInbox, inbox.data?.teams, inbox.data?.views, menu, navigate, onShowShortcuts, slug, t])

  const workspaceActions = useMemo<NavAction[]>(() => {
    if (impersonation) return []
    return activeWorkspaces
      .filter((ws) => ws.slug !== slug)
      .map((ws) => ({
        id: `workspace:${ws.slug}`,
        label: ws.name,
        description: `/app/${ws.slug}/`,
        icon: <IconSwitchHorizontal size={16} stroke={1.6} />,
        onClick: () => void switchTo(ws.slug),
      }))
  }, [activeWorkspaces, impersonation, slug, switchTo])

  const kw = trimmed.toLowerCase()
  const matchesLocal = (a: NavAction) =>
    !kw || a.label.toLowerCase().includes(kw) || (a.description?.toLowerCase().includes(kw) ?? false)
  const filteredConvActions = kw ? conversationActions.filter(matchesLocal) : conversationActions.filter((a) => a.quick)
  const filteredInbox = kw ? inboxActions.filter(matchesLocal) : inboxActions
  const filteredPages = kw ? allPageActions.filter(matchesLocal) : quickPageActions
  const filteredWorkspaces = kw ? workspaceActions.filter(matchesLocal) : workspaceActions
  const convHits = debounced ? (convSearch.data?.items ?? []) : []
  const convTotal = debounced ? (convSearch.data?.total ?? 0) : 0
  const contactHits = debounced ? (contactSearch.data?.items ?? []) : []
  const chatHits = debounced ? (chatSearch.data?.items ?? []) : []
  const ticketHits = useMemo(() => {
    if (!debounced) return []
    const items = ticketSearch.data?.items ?? []
    const exact = ticketExact.data
    if (!exact) return items
    return [exact, ...items.filter((x) => x.id !== exact.id)]
  }, [debounced, ticketExact.data, ticketSearch.data?.items])
  const remotePending =
    trimmed.length > 0 &&
    (canReadInbox || canReadContacts) &&
    (debounced !== trimmed ||
      convSearch.isFetching ||
      contactSearch.isFetching ||
      ticketSearch.isFetching ||
      ticketExact.isFetching ||
      chatSearch.isFetching)
  const hasAny =
    filteredConvActions.length > 0 ||
    chatHits.length > 0 ||
    ticketHits.length > 0 ||
    convHits.length > 0 ||
    contactHits.length > 0 ||
    filteredInbox.length > 0 ||
    filteredPages.length > 0 ||
    filteredWorkspaces.length > 0

  return (
    <Spotlight.Root
      className="cmd-palette"
      query={query}
      onQueryChange={setQuery}
      onSpotlightClose={() => setQuery("")}
      shortcut="mod + K"
      size={580}
      yOffset="12vh"
      radius="lg"
      shadow="xl"
      scrollable
      maxHeight={440}
    >
      <Spotlight.Search
        size="lg"
        leftSection={<IconSearch size={16} stroke={1.6} />}
        leftSectionWidth={46}
        rightSectionWidth={44}
        rightSection={
          remotePending ? (
            <Loader size={14} />
          ) : query.length > 0 ? (
            <CloseButton size="sm" variant="subtle" aria-label={t("common.clear_search")} onClick={() => setQuery("")} />
          ) : undefined
        }
        rightSectionPointerEvents={query.length > 0 && !remotePending ? "auto" : "none"}
        placeholder={t("command.placeholder")}
      />

      <Spotlight.ActionsList>
        {filteredConvActions.length > 0 && (
          <Spotlight.ActionsGroup label={t("command.conversation_actions_group")}>
            {filteredConvActions.map((a) => (
              <Spotlight.Action key={a.id} onClick={a.onClick}>
                <Group gap="sm" wrap="nowrap" w="100%">
                  <Box className="cmd-palette-icon">{a.icon}</Box>
                  <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                    <HighlightQuery text={a.label} query={query} />
                  </Text>
                  {a.description && (
                    <Text size="xs" c="dimmed" truncate maw={180} style={{ flex: "none" }}>
                      {a.description}
                    </Text>
                  )}
                </Group>
              </Spotlight.Action>
            ))}
          </Spotlight.ActionsGroup>
        )}

        {chatHits.length > 0 && (
          <Spotlight.ActionsGroup label={t("nav.chat")}>
            {chatHits.map((ch) => (
              <Spotlight.Action
                key={`chat:${ch.id}`}
                onClick={() => void navigate({ to: "/$slug/inbox/chat/$id", params: { slug, id: ch.id } })}
              >
                <Group gap="sm" wrap="nowrap" w="100%">
                  <Box className="cmd-palette-icon">
                    {ch.mode === "group" ? <IconUsersGroup size={16} stroke={1.6} /> : <IconMessageCircle size={16} stroke={1.6} />}
                  </Box>
                  <Box style={{ flex: 1, minWidth: 0 }}>
                    <Group gap={6} wrap="nowrap">
                      <Text size="sm" fw={500} truncate>
                        <HighlightQuery text={chatDisplayName(ch, t)} query={debounced} />
                      </Text>
                      <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                        {t(ch.mode === "group" ? "chat.mode_group" : "chat.mode_direct")}
                      </Badge>
                    </Group>
                    {ch.preview && (
                      <Text size="xs" c="dimmed" truncate>
                        {ch.preview}
                      </Text>
                    )}
                  </Box>
                </Group>
              </Spotlight.Action>
            ))}
          </Spotlight.ActionsGroup>
        )}

        {ticketHits.length > 0 && (
          <Spotlight.ActionsGroup label={t("tickets.command_group")}>
            {ticketHits.map((tk) => (
              <Spotlight.Action
                key={`ticket:${tk.id}`}
                onClick={() =>
                  void navigate({
                    to: "/$slug/inbox/tickets/$id",
                    params: { slug, id: tk.id },
                    search: { box: "all", status: "unresolved" },
                  })
                }
              >
                <Group gap="sm" wrap="nowrap" w="100%">
                  <Box className="cmd-palette-icon">
                    <IconTicket size={16} stroke={1.6} />
                  </Box>
                  <Box style={{ flex: 1, minWidth: 0 }}>
                    <Group gap={6} wrap="nowrap">
                      <Text size="xs" fw={600} c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                        #{tk.number}
                      </Text>
                      <Text size="sm" fw={500} truncate>
                        <HighlightQuery text={tk.title} query={debounced} />
                      </Text>
                      <Badge size="xs" variant="light" color={ticketStatusColor[tk.status]} style={{ flex: "none" }}>
                        {ticketStatusLabel(tk.status, t)}
                      </Badge>
                    </Group>
                    <Text size="xs" c="dimmed" truncate>
                      {[tk.type_name, tk.linked_count > 0 ? t("tickets.linked_count", { n: tk.linked_count }) : undefined]
                        .filter(Boolean)
                        .join(" · ")}
                    </Text>
                  </Box>
                </Group>
              </Spotlight.Action>
            ))}
          </Spotlight.ActionsGroup>
        )}

        {convHits.length > 0 && (
          <Spotlight.ActionsGroup label={t("command.conversations_group")}>
            {convHits.map((hit) => {
              const c = hit.conversation
              const m = hit.match
              return (
                <Spotlight.Action
                  key={`conv:${c.id}:${m.item_id}`}
                  onClick={() =>
                    void navigate({
                      to: "/$slug/inbox/$id",
                      params: { slug, id: c.id },
                      search: {
                        box: "all",
                        status: c.status,
                        sort: "waiting",
                        q: debounced,
                        scope: "all",
                        seq: m.seq,
                        matchField: m.field as MatchField,
                      },
                    })
                  }
                >
                  <Group gap="sm" wrap="nowrap" w="100%" align="flex-start">
                    <Box className="cmd-palette-icon" mt={2}>
                      {m.kind === "note" ? <IconLock size={16} stroke={1.6} /> : <IconMessage size={16} stroke={1.6} />}
                    </Box>
                    <Box style={{ flex: 1, minWidth: 0 }}>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" fw={600} truncate>
                          {contactDisplayName(c.contact, t)}
                        </Text>
                        {c.channel && (
                          <Badge size="xs" variant="outline" color="gray" style={{ flex: "none" }}>
                            {c.channel.name}
                          </Badge>
                        )}
                        <Badge size="xs" variant="light" color={conversationStatusColor[c.status]} style={{ flex: "none" }}>
                          {t(`inbox.status_${c.status}` as const)}
                        </Badge>
                        {hit.match_count > 1 && (
                          <Text size="xs" c="brand" style={{ flex: "none" }}>
                            {t("inbox.search_match_count", { n: hit.match_count })}
                          </Text>
                        )}
                        <Text size="xs" c="dimmed" ml="auto" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                          {activity(m.created_at, language, t, zone)}
                        </Text>
                      </Group>
                      <Text size="xs" c="dimmed" lineClamp={2} mt={2}>
                        <HighlightedSnippet parts={m.snippet} />
                      </Text>
                    </Box>
                  </Group>
                </Spotlight.Action>
              )
            })}
            {convTotal > convHits.length && (
              <Spotlight.Action
                onClick={() =>
                  void navigate({
                    to: "/$slug/inbox",
                    params: { slug },
                    search: { box: "all", status: "open", sort: "waiting", q: debounced, scope: "all" },
                  })
                }
              >
                <Group gap="xs" c="brand" px={4}>
                  <IconArrowRight size={16} stroke={1.6} />
                  <Text size="xs" fw={600}>
                    {t("command.view_all_results", { n: convTotal })}
                  </Text>
                </Group>
              </Spotlight.Action>
            )}
          </Spotlight.ActionsGroup>
        )}

        {contactHits.length > 0 && (
          <Spotlight.ActionsGroup label={t("command.contacts_group")}>
            {contactHits.map((ct) => {
              const displayName = contactDisplayName(ct, t)
              const subtitle = contactSubline(ct)
              return (
                <Spotlight.Action
                  key={`contact:${ct.id}`}
                  onClick={() =>
                    void navigate({
                      to: "/$slug/contacts/$id",
                      params: { slug, id: ct.id },
                      search: { sort: "recent" },
                    })
                  }
                >
                  <Group gap="sm" wrap="nowrap" w="100%">
                    <Box className="cmd-palette-icon">
                      <IconUser size={16} stroke={1.6} />
                    </Box>
                    <Box style={{ flex: 1, minWidth: 0 }}>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" fw={500} truncate>
                          <HighlightQuery text={displayName} query={debounced} />
                        </Text>
                        <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                          {kit.label("ContactKind", ct.kind)}
                        </Badge>
                      </Group>
                      {subtitle && (
                        <Text size="xs" c="dimmed" truncate>
                          <HighlightQuery text={subtitle} query={debounced} />
                        </Text>
                      )}
                    </Box>
                  </Group>
                </Spotlight.Action>
              )
            })}
          </Spotlight.ActionsGroup>
        )}

        {filteredInbox.length > 0 && (
          <Spotlight.ActionsGroup label={t("command.inbox_group")}>
            {filteredInbox.map((a) => (
              <Spotlight.Action key={a.id} onClick={a.onClick}>
                <Group gap="sm" wrap="nowrap" w="100%">
                  <Box className="cmd-palette-icon">{a.icon}</Box>
                  <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                    <HighlightQuery text={a.label} query={query} />
                  </Text>
                  {a.description && (
                    <Text size="xs" c="dimmed" style={{ flex: "none" }}>
                      {a.description}
                    </Text>
                  )}
                </Group>
              </Spotlight.Action>
            ))}
          </Spotlight.ActionsGroup>
        )}

        {filteredPages.length > 0 && (
          <Spotlight.ActionsGroup label={t("command.pages_group")}>
            {filteredPages.map((a) => (
              <Spotlight.Action key={a.id} onClick={a.onClick}>
                <Group gap="sm" wrap="nowrap" w="100%">
                  <Box className="cmd-palette-icon">{a.icon}</Box>
                  <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                    <HighlightQuery text={a.label} query={query} />
                  </Text>
                  {a.description && (
                    <Text size="xs" c="dimmed" truncate maw={220} style={{ flex: "none" }}>
                      {a.description}
                    </Text>
                  )}
                </Group>
              </Spotlight.Action>
            ))}
          </Spotlight.ActionsGroup>
        )}

        {filteredWorkspaces.length > 0 && (
          <Spotlight.ActionsGroup label={t("command.workspaces_group")}>
            {filteredWorkspaces.map((a) => (
              <Spotlight.Action key={a.id} onClick={a.onClick}>
                <Group gap="sm" wrap="nowrap" w="100%">
                  <Box className="cmd-palette-icon">{a.icon}</Box>
                  <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                    <HighlightQuery text={a.label} query={query} />
                  </Text>
                  {a.description && (
                    <Text size="xs" c="dimmed" truncate maw={220} style={{ flex: "none" }}>
                      {a.description}
                    </Text>
                  )}
                </Group>
              </Spotlight.Action>
            ))}
          </Spotlight.ActionsGroup>
        )}

        {!hasAny && remotePending && (
          <Box className="cmd-palette-state">
            <Loader size="sm" />
            <Text size="xs" c="dimmed">
              {t("command.searching")}
            </Text>
          </Box>
        )}

        {!hasAny && !remotePending && (
          <Box className="cmd-palette-state">
            <IconSearch size={24} stroke={1.6} />
            <Text size="sm" c="dimmed">
              {t("command.empty")}
            </Text>
          </Box>
        )}
      </Spotlight.ActionsList>

      <Spotlight.Footer>
        <Group justify="space-between" align="center" gap="md">
          <Group gap="md">
            <Group gap={6}>
              <Kbd size="xs">↑↓</Kbd>
              <Text size="xs" c="dimmed">
                {t("command.hint_navigate")}
              </Text>
            </Group>
            <Group gap={6}>
              <Kbd size="xs">↵</Kbd>
              <Text size="xs" c="dimmed">
                {t("command.hint_open")}
              </Text>
            </Group>
            <Group gap={6}>
              <Kbd size="xs">Esc</Kbd>
              <Text size="xs" c="dimmed">
                {t("command.hint_close")}
              </Text>
            </Group>
          </Group>
          <UnstyledButton
            onClick={() => {
              spotlight.close()
              onShowShortcuts()
            }}
          >
            <Group gap={6}>
              <Kbd size="xs">?</Kbd>
              <Text size="xs" c="dimmed">
                {t("command.hint_shortcuts")}
              </Text>
            </Group>
          </UnstyledButton>
        </Group>
      </Spotlight.Footer>
    </Spotlight.Root>
  )
}
