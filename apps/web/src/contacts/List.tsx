import type { ContactSort } from "@agents-desk/client/tenant"
import { ActionIcon, Avatar, Badge, Box, Button, Center, Group, Loader, Select, Skeleton, Stack, Text, TextInput, Tooltip } from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useInfiniteQuery, useQuery } from "@tanstack/react-query"
import { useNavigate, useParams } from "@tanstack/react-router"
import { IconLayoutSidebarLeftExpand, IconMessageCircle, IconSearch, IconX } from "@tabler/icons-react"
import { useLanguage, useNarrow, useNavControl, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { activity } from "../inbox/format"
import { languageName } from "../languages"
import { NARROW_QUERY } from "../layout"
import { formatLocalTime } from "../timezone"
import type { ContactsSearch } from "./search"

/** FILTER_LANGUAGES 是联系人列表语言筛选的三种常用语言：中文、英语、印地语（后端按主语言前缀同时匹配 zh-CN、hi-Latn 等）。 */
const FILTER_LANGUAGES = ["zh", "en", "hi"] as const

/**
 * ContactDirectory 是左侧的联系人名录：顶部一个整体标题与筛选块（第一行是分类标题和数量，第二行是搜索、语言、渠道和排序，
 * 中间不加多余横线，风格与收件箱列表顶栏一致）；下方游标分页、滚到底自动加载下一页；点击整行在右侧分栏展开或切换档案。
 */
export function ContactDirectory({
  search,
  openId,
  go,
}: {
  search: ContactsSearch
  openId?: string
  go: (patch: Partial<ContactsSearch>) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const narrow = useNarrow(NARROW_QUERY)
  const nav = useNavControl()
  const { slug } = useParams({ from: "/t/$slug" })
  const navigate = useNavigate()

  const [keyword, setKeyword] = useState(search.q ?? "")
  const [debounced] = useDebouncedValue(keyword, 250)

  useEffect(() => {
    setKeyword(search.q ?? "")
  }, [search.q])

  useEffect(() => {
    const trimmed = debounced.trim() || undefined
    if (trimmed !== search.q) {
      go({ q: trimmed })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debounced])

  const summary = useQuery(kit.queries.contact.summary())
  const list = useInfiniteQuery({
    queryKey: [
      ...kit.queries.contact.key(),
      "cursor-list",
      search.kind ?? "",
      search.q ?? "",
      search.language ?? "",
      search.channel ?? "",
      search.sort,
    ],
    queryFn: ({ pageParam }) =>
      tenantApi.contact.list({
        kind: search.kind,
        q: search.q,
        language: search.language,
        channel_id: search.channel,
        sort: search.sort,
        cursor: pageParam || undefined,
        per_page: 50,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  })

  // 滚到底时自动加载下一页（与收件箱列表一致）
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = end.current
    if (!el) return
    const io = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage()
    })
    io.observe(el)
    return () => io.disconnect()
  }, [list])

  const items = (list.data?.pages ?? []).flatMap((p) => p.items)

  // 从收件箱等外部直达 /contacts/$id 时，尽量把目标联系人滚到列表最上方（避开吸顶表头）；
  // 在列表内手动点击某一行时不额外跳动滚动位置；若目标不在当前已加载页中，自动续拉（最多 5 页）直到找到
  const rowRefs = useRef(new Map<string, HTMLDivElement>())
  const scrolledFor = useRef<string | undefined>(undefined)
  const clickedId = useRef<string | undefined>(undefined)
  const autoPages = useRef(0)
  useEffect(() => {
    autoPages.current = 0
    scrolledFor.current = undefined
  }, [openId, search.kind, search.q, search.language, search.channel, search.sort])
  useEffect(() => {
    if (!openId || scrolledFor.current === openId) return
    if (clickedId.current === openId) {
      clickedId.current = undefined
      scrolledFor.current = openId
      return
    }
    const el = rowRefs.current.get(openId)
    if (el) {
      el.scrollIntoView({ block: "start" })
      scrolledFor.current = openId
      return
    }
    if (!list.isPending && list.hasNextPage && !list.isFetchingNextPage && autoPages.current < 5) {
      autoPages.current += 1
      void list.fetchNextPage()
    }
  }, [openId, items.length, list])

  const title = search.kind === "user" ? t("contacts.nav_users") : search.kind === "lead" ? t("contacts.nav_leads") : t("nav.contacts")
  const channels = (summary.data?.channels ?? []).map((ch) => ({ value: ch.id, label: ch.name }))
  const languageChoices = FILTER_LANGUAGES.map((tag) => ({ value: tag, label: languageName(tag, ui) }))
  const sorts = kit.enums.ContactSort.values.map((v) => ({ value: v, label: kit.label("ContactSort", v) }))
  const hasFilters = Boolean(search.q || search.language || search.channel)
  const summaryCount = summary.data
    ? search.kind === "user"
      ? summary.data.users
      : search.kind === "lead"
        ? summary.data.leads
        : summary.data.all
    : undefined
  const badgeCount = hasFilters ? (list.data ? `${items.length}${list.hasNextPage ? "+" : ""}` : undefined) : summaryCount
  // 右侧分栏打开时空间收窄，自动隐藏中间列（右侧档案栏中已经完整展示地区与语言）
  const compact = narrow || Boolean(openId)

  const openContact = (id: string) => {
    clickedId.current = id
    void navigate({ to: "/t/$slug/contacts/$id", params: { slug, id }, search })
  }

  return (
    <Box style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* 52px 基准顶栏：与二级导航头和右侧档案顶栏严格齐平 */}
      <Group gap="xs" px="md" wrap="nowrap" className="ct-header">
        {nav && !nav.open && (
          <Tooltip label={t("nav.expand_nav")} openDelay={350}>
            <ActionIcon variant="subtle" color="gray" onClick={nav.openNav} aria-label={t("nav.expand_nav")}>
              <IconLayoutSidebarLeftExpand size={18} stroke={1.6} />
            </ActionIcon>
          </Tooltip>
        )}
        <Text fw={600} size="sm" truncate>
          {title}
        </Text>
        {badgeCount !== undefined && (
          <Badge size="xs" variant="light" color="gray" radius="sm" style={{ flex: "none" }}>
            {badgeCount}
          </Badge>
        )}
        {hasFilters && (
          <Button
            size="compact-xs"
            variant="subtle"
            color="gray"
            ml="auto"
            onClick={() => {
              setKeyword("")
              go({ q: undefined, language: undefined, channel: undefined })
            }}
          >
            {t("contacts.clear_filters")}
          </Button>
        )}
      </Group>

      {/* 独立搜索与筛选工具条 */}
      <Group gap="xs" px="md" py={8} wrap="wrap" className="ct-toolbar">
        <TextInput
          size="xs"
          style={{ flex: 1, minWidth: 200 }}
          value={keyword}
          onChange={(e) => setKeyword(e.currentTarget.value)}
          placeholder={t("contacts.search_placeholder")}
          leftSection={<IconSearch size={14} />}
          rightSection={
            keyword ? (
              <ActionIcon variant="subtle" color="gray" size="xs" onClick={() => setKeyword("")} aria-label={t("common.cancel")}>
                <IconX size={12} />
              </ActionIcon>
            ) : null
          }
        />
        <Select
          size="xs"
          w={compact ? 108 : 124}
          clearable
          placeholder={t("contacts.filter_language")}
          data={languageChoices}
          value={search.language ?? null}
          onChange={(v) => go({ language: v || undefined })}
          aria-label={t("contacts.language")}
        />
        {channels.length > 0 && (
          <Select
            size="xs"
            w={compact ? 116 : 140}
            clearable
            placeholder={t("contacts.filter_channel")}
            data={channels}
            value={search.channel ?? null}
            onChange={(v) => go({ channel: v || undefined })}
            aria-label={t("contacts.filter_channel")}
          />
        )}
        <Select
          size="xs"
          w={108}
          allowDeselect={false}
          data={sorts}
          value={search.sort}
          onChange={(v) => v && go({ sort: v as ContactSort })}
          aria-label={t("contacts.sort")}
        />
      </Group>

      <Box style={{ flex: 1, minHeight: 0, overflowY: "auto" }}>
        {!narrow && items.length > 0 && (
          <Group gap="md" px="lg" py={7} wrap="nowrap" className="ct-list-head">
            <Text size="xs" c="dimmed" fw={500} style={{ flex: 2.2, minWidth: 200 }}>
              {t("contacts.col_contact")}
            </Text>
            {!compact && (
              <Text size="xs" c="dimmed" fw={500} style={{ flex: 1.4, minWidth: 160 }}>
                {t("contacts.col_location_lang")}
              </Text>
            )}
            <Text size="xs" c="dimmed" fw={500} w={72} ta="right" style={{ flex: "none" }}>
              {t("contacts.col_conversations")}
            </Text>
            <Text size="xs" c="dimmed" fw={500} w={96} ta="right" style={{ flex: "none" }}>
              {t("contacts.col_last_seen")}
            </Text>
          </Group>
        )}

        {list.isPending ? (
          <Stack gap={0}>
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <Group key={i} gap="md" px="lg" py={11} wrap="nowrap" className="ct-row">
                <Group gap="sm" wrap="nowrap" style={{ flex: 2.2, minWidth: 200 }}>
                  <Skeleton circle h={34} w={34} style={{ flex: "none" }} />
                  <Box style={{ flex: 1, minWidth: 0 }}>
                    <Skeleton h={12} w="42%" mb={6} radius="sm" />
                    <Skeleton h={10} w="64%" radius="sm" />
                  </Box>
                </Group>
                {!compact && <Skeleton h={11} w="20%" radius="sm" />}
                <Skeleton h={11} w={36} radius="sm" style={{ flex: "none" }} />
                <Skeleton h={11} w={56} radius="sm" style={{ flex: "none" }} />
              </Group>
            ))}
          </Stack>
        ) : items.length === 0 ? (
          <Center py={64}>
            <Stack gap="xs" align="center">
              <Text size="sm" c="dimmed">
                {t(hasFilters ? "contacts.empty_filtered" : "contacts.empty")}
              </Text>
              {hasFilters && (
                <Button
                  size="xs"
                  variant="light"
                  onClick={() => {
                    setKeyword("")
                    go({ q: undefined, language: undefined, channel: undefined })
                  }}
                >
                  {t("contacts.clear_filters")}
                </Button>
              )}
            </Stack>
          </Center>
        ) : (
          <>
            {items.map((c) => {
              const displayName = c.name || t("inbox.visitor")
              const localTime = formatLocalTime(c.time_zone)
              const place = [c.location, localTime ? t("contacts.local_time", { time: localTime }) : ""].filter(Boolean).join(" · ")
              const langLabel = c.language ? languageName(c.language, ui) : ""
              const subline = [c.email, c.external_user_id].filter(Boolean).join(" · ")
              const active = c.id === openId
              return (
                <Group
                  key={c.id}
                  ref={(node) => {
                    if (node) rowRefs.current.set(c.id, node)
                    else rowRefs.current.delete(c.id)
                  }}
                  role="button"
                  tabIndex={0}
                  gap="md"
                  px="lg"
                  py={11}
                  wrap={narrow ? "wrap" : "nowrap"}
                  align="center"
                  className="ct-row"
                  data-active={active || undefined}
                  onClick={() => openContact(c.id)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      openContact(c.id)
                    }
                  }}
                >
                  <Group gap="sm" wrap="nowrap" style={{ flex: narrow ? "1 1 100%" : 2.2, minWidth: 200 }}>
                    <Avatar size={34} name={displayName} color="initials" />
                    <Box style={{ minWidth: 0, flex: 1 }}>
                      <Group gap={6} wrap="nowrap">
                        <Text size="sm" fw={500} truncate>
                          {displayName}
                        </Text>
                        <Badge size="xs" variant="light" color={c.kind === "user" ? "bright-blue" : "gray"} style={{ flex: "none" }}>
                          {kit.label("ContactKind", c.kind)}
                        </Badge>
                      </Group>
                      {subline && (
                        <Text size="xs" c="dimmed" truncate mt={1}>
                          {subline}
                        </Text>
                      )}
                    </Box>
                  </Group>

                  {!compact && (
                    <Box style={{ flex: 1.4, minWidth: 160 }}>
                      {place && (
                        <Text size="xs" truncate>
                          {place}
                        </Text>
                      )}
                      {langLabel && (
                        <Text size="xs" c="dimmed" truncate>
                          {langLabel}
                        </Text>
                      )}
                      {!place && !langLabel && (
                        <Text size="xs" c="dimmed">
                          —
                        </Text>
                      )}
                    </Box>
                  )}

                  <Group gap={4} justify="flex-end" wrap="nowrap" w={narrow ? "auto" : 72} style={{ flex: "none" }}>
                    <IconMessageCircle size={13} style={{ color: "var(--mantine-color-dimmed)" }} />
                    <Text size="xs" c={c.conversation_count > 0 ? undefined : "dimmed"} style={{ fontVariantNumeric: "tabular-nums" }}>
                      {c.conversation_count}
                    </Text>
                  </Group>

                  <Text
                    size="xs"
                    c="dimmed"
                    w={narrow ? "auto" : 96}
                    ta="right"
                    ml={narrow ? "auto" : undefined}
                    style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}
                  >
                    {activity(c.last_seen_at ?? c.created_at, ui, t, zone)}
                  </Text>
                </Group>
              )
            })}
            <Box ref={end} py={list.isFetchingNextPage ? "md" : 4}>
              {list.isFetchingNextPage && (
                <Center>
                  <Loader size="xs" />
                </Center>
              )}
            </Box>
          </>
        )}
      </Box>
    </Box>
  )
}
