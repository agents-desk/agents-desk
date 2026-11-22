import { Badge, Box, Group, Loader, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { useDebouncedValue, useOs } from "@mantine/hooks"
import { Spotlight, spotlight } from "@mantine/spotlight"
import {
  IconArrowRight,
  IconAt,
  IconInbox,
  IconListDetails,
  IconLock,
  IconMessage,
  IconSearch,
  IconUser,
  IconUserQuestion,
  IconUsersGroup,
} from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Icon, type MenuItem, useCan, useLanguage, useNavigation, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useMemo, useState } from "react"
import { kit, tenantApi } from "./api"
import { activity } from "./inbox/format"
import { HighlightedSnippet, HighlightQuery } from "./inbox/Highlight"
import type { MatchField } from "./inbox/search"

/** pages 将菜单树拍平为叶子页面列表，保留上级分组名称和最近图标。 */
function flattenPages(
  items: readonly MenuItem[],
  trail: string[] = [],
  icon?: string,
): { item: MenuItem; trail: string[]; icon?: string }[] {
  return items.flatMap((item) =>
    item.children?.length ? flattenPages(item.children, [...trail, item.title], item.icon ?? icon) : [{ item, trail, icon: item.icon ?? icon }],
  )
}

/** statusColor 是会话状态的语义徽标颜色。 */
const statusColor = { open: "bright-blue", snoozed: "yellow", closed: "gray" } as const

/** CommandSearchButton 是放在图标栏顶部的全局搜索入口，点击或按 Cmd/Ctrl + K 打开命令面板。 */
export function CommandSearchButton(): ReactNode {
  const t = useT()
  const os = useOs()
  const shortcut = os === "macos" || os === "ios" ? "⌘K" : "Ctrl+K"
  return (
    <Tooltip label={`${t("command.search")}  ${shortcut}`} position="right" openDelay={350}>
      <UnstyledButton className="tn-rail-module" aria-label={t("command.search")} onClick={spotlight.open}>
        <IconSearch size={18} stroke={1.6} />
      </UnstyledButton>
    </Tooltip>
  )
}

interface NavAction {
  id: string
  label: string
  description?: string
  icon: ReactNode
  onClick: () => void
}

/**
 * CommandPalette 是工作台的全局命令面板（Cmd/Ctrl + K）：
 * 支持按关键词实时全文搜索会话（含高亮摘要）和联系人，同时支持快速跳转收件箱视图、团队收件箱与有权限访问的页面。
 */
export function CommandPalette({ slug }: { slug: string }): ReactNode {
  const t = useT()
  const can = useCan()
  const language = useLanguage()
  const zone = useTimeZone()
  const { menu } = useNavigation()
  const navigate = useNavigate()
  const [query, setQuery] = useState("")
  const [debounced] = useDebouncedValue(query.trim(), 250)
  const canReadInbox = can("conversations:read")
  const canReadContacts = can("contacts:read")
  const inbox = useQuery({ ...kit.queries.conversation.inbox(), enabled: canReadInbox })

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

  const { inboxActions, pageActions } = useMemo(() => {
    const inboxList: NavAction[] = []
    if (canReadInbox) {
      const boxSearch = { status: "open" as const, sort: "waiting" as const }
      inboxList.push(
        {
          id: "inbox:mine",
          label: t("inbox.box_mine"),
          description: t("nav.inbox"),
          icon: <IconInbox size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "mine" } }),
        },
        {
          id: "inbox:mentions",
          label: t("inbox.box_mentions"),
          description: t("nav.inbox"),
          icon: <IconAt size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "mentions" } }),
        },
        {
          id: "inbox:unassigned",
          label: t("inbox.box_unassigned"),
          description: t("nav.inbox"),
          icon: <IconUserQuestion size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "unassigned" } }),
        },
        {
          id: "inbox:all",
          label: t("inbox.box_all"),
          description: t("nav.inbox"),
          icon: <IconListDetails size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "all" } }),
        },
        ...(inbox.data?.teams ?? []).map((team) => ({
          id: `inbox:team:${team.id}`,
          label: team.name,
          description: t("inbox.teams"),
          icon: <IconUsersGroup size={16} stroke={1.6} />,
          onClick: () =>
            void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "all", team: team.id } }),
        })),
      )
    }
    const pagesList: NavAction[] = flattenPages(menu).map(({ item, trail, icon }) => ({
      id: `page:${item.id}`,
      label: item.title,
      description: trail.length > 0 ? trail.join(" / ") : undefined,
      icon: <Icon name={icon ?? "apps"} size={16} stroke={1.6} />,
      onClick: () => void navigate({ to: item.path }),
    }))
    return { inboxActions: inboxList, pageActions: pagesList }
  }, [canReadInbox, inbox.data?.teams, menu, navigate, slug, t])

  const kw = query.trim().toLowerCase()
  const matchesLocal = (a: NavAction) =>
    !kw || a.label.toLowerCase().includes(kw) || (a.description?.toLowerCase().includes(kw) ?? false)
  const filteredInbox = inboxActions.filter(matchesLocal)
  const filteredPages = pageActions.filter(matchesLocal)
  const convHits = debounced ? (convSearch.data?.items ?? []) : []
  const convTotal = debounced ? (convSearch.data?.total ?? 0) : 0
  const contactHits = debounced ? (contactSearch.data?.items ?? []) : []
  const loadingRemote = Boolean(debounced && (convSearch.isFetching || contactSearch.isFetching))
  const hasAny = convHits.length > 0 || contactHits.length > 0 || filteredInbox.length > 0 || filteredPages.length > 0

  return (
    <Spotlight.Root
      query={query}
      onQueryChange={setQuery}
      onSpotlightClose={() => setQuery("")}
      shortcut="mod + K"
      scrollable
      maxHeight={460}
    >
      <Spotlight.Search
        leftSection={<IconSearch size={16} stroke={1.6} />}
        rightSection={loadingRemote ? <Loader size={14} /> : undefined}
        placeholder={t("command.placeholder")}
      />
      <Spotlight.ActionsList>
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
                      to: "/t/$slug/inbox/$id",
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
                    <Box mt={2} c="dimmed" style={{ flex: "none" }}>
                      {m.kind === "note" ? <IconLock size={16} stroke={1.6} /> : <IconMessage size={16} stroke={1.6} />}
                    </Box>
                    <Box style={{ flex: 1, minWidth: 0 }}>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" fw={600} truncate>
                          {c.contact?.name || t("inbox.visitor")}
                        </Text>
                        {c.channel && (
                          <Badge size="xs" variant="outline" color="gray" style={{ flex: "none" }}>
                            {c.channel.name}
                          </Badge>
                        )}
                        <Badge size="xs" variant="light" color={statusColor[c.status]} style={{ flex: "none" }}>
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
                    to: "/t/$slug/inbox",
                    params: { slug },
                    search: { box: "all", status: "open", sort: "waiting", q: debounced, scope: "all" },
                  })
                }
              >
                <Group gap="xs" c="brand">
                  <IconArrowRight size={15} stroke={1.8} />
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
              const subtitle = [ct.email, ct.external_user_id].filter(Boolean).join(" · ")
              return (
                <Spotlight.Action
                  key={`contact:${ct.id}`}
                  onClick={() =>
                    void navigate({
                      to: "/t/$slug/contacts/$id",
                      params: { slug, id: ct.id },
                      search: { sort: "recent" },
                    })
                  }
                >
                  <Group gap="sm" wrap="nowrap" w="100%">
                    <Box c="dimmed" style={{ flex: "none" }}>
                      <IconUser size={16} stroke={1.6} />
                    </Box>
                    <Box style={{ flex: 1, minWidth: 0 }}>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" fw={500} truncate>
                          <HighlightQuery text={ct.name || t("inbox.visitor")} query={debounced} />
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
                  <Box c="dimmed" style={{ flex: "none" }}>
                    {a.icon}
                  </Box>
                  <Box style={{ flex: 1, minWidth: 0 }}>
                    <Text size="sm" truncate>
                      <HighlightQuery text={a.label} query={query} />
                    </Text>
                    {a.description && (
                      <Text size="xs" c="dimmed" truncate>
                        {a.description}
                      </Text>
                    )}
                  </Box>
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
                  <Box c="dimmed" style={{ flex: "none" }}>
                    {a.icon}
                  </Box>
                  <Box style={{ flex: 1, minWidth: 0 }}>
                    <Text size="sm" truncate>
                      <HighlightQuery text={a.label} query={query} />
                    </Text>
                    {a.description && (
                      <Text size="xs" c="dimmed" truncate>
                        {a.description}
                      </Text>
                    )}
                  </Box>
                </Group>
              </Spotlight.Action>
            ))}
          </Spotlight.ActionsGroup>
        )}

        {!hasAny && !loadingRemote && <Spotlight.Empty>{t("command.empty")}</Spotlight.Empty>}
      </Spotlight.ActionsList>
    </Spotlight.Root>
  )
}
