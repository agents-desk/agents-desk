import type { ContactSort } from "@agents-desk/client/tenant"
import { Badge, Box, Button, Center, CloseButton, Group, Loader, Select, Skeleton, Stack, Text, TextInput } from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { infiniteQueryOptions, useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useParams } from "@tanstack/react-router"
import { IconBookmarkPlus, IconMessageCircle, IconSearch } from "@tabler/icons-react"
import { ExportButton, PaneBody, PaneHeader, PaneToolbar, useCan, useLanguage, useNarrow, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { useTags } from "../inbox/data"
import { activity } from "../inbox/format"
import { LanguageSelect } from "../LanguageSelect"
import { languageName } from "../languages"
import { NARROW_QUERY } from "../layout"
import { formatLocalTime } from "../timezone"
import { ContactAvatar, contactDisplayName, contactSubline } from "./display"
import type { ContactsSearch } from "./search"
import { ContactViewModal } from "./ViewModal"
import { EmptyState } from "../components/empty-state"
import { prefetchContact } from "./Detail"

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
  const can = useCan()
  const ui = useLanguage()
  const zone = useTimeZone()
  const narrow = useNarrow(NARROW_QUERY)
  const { slug } = useParams({ from: "/$slug" })
  const navigate = useNavigate()
  const tags = useTags()

  const [keyword, setKeyword] = useState(search.q ?? "")
  const [debounced] = useDebouncedValue(keyword, 250)
  const [saveViewOpen, setSaveViewOpen] = useState(false)

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

  const queryClient = useQueryClient()
  const summary = useQuery(kit.queries.contact.summary())
  const activeView = search.view ? (summary.data?.views ?? []).find((v) => v.id === search.view) : undefined
  const list = useInfiniteQuery(contactListQuery(search))

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
  }, [openId, search.view, search.kind, search.q, search.language, search.channel, search.tag, search.sort])
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

  const title = activeView
    ? activeView.name
    : search.kind === "user"
      ? t("contacts.nav_users")
      : search.kind === "lead"
        ? t("contacts.nav_leads")
        : t("nav.contacts")
  const channels = (summary.data?.channels ?? []).map((ch) => ({ value: ch.id, label: ch.name }))
  const tagChoices = tags.map((tg) => ({ value: tg.id, label: tg.name }))
  const activeLanguages = summary.data?.languages ?? []
  const sorts = kit.enums.ContactSort.values.map((v) => ({ value: v, label: kit.label("ContactSort", v) }))
  const hasFilters = Boolean(search.q || search.language || search.channel || search.tag)
  const summaryCount = activeView
    ? activeView.count
    : summary.data
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
    void navigate({ to: "/$slug/contacts/$id", params: { slug, id }, search })
  }

  return (
    <Box style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* 52px 基准顶栏：与二级导航头和右侧档案顶栏严格齐平 */}
      <PaneHeader
        showNavToggle
        title={
          <Group gap="xs" wrap="nowrap">
            <Text fw={600} size="sm" truncate>
              {title}
            </Text>
            {badgeCount !== undefined && (
              <Badge size="xs" variant="light" color="gray" radius="sm" style={{ flex: "none" }}>
                {badgeCount}
              </Badge>
            )}
          </Group>
        }
        actions={
          <Group gap={6} wrap="nowrap" style={{ flex: "none" }} data-tn-density="dense">
            {hasFilters && (
              <Button
                size="compact-xs"
                variant="subtle"
                color="gray"
                onClick={() => {
                  setKeyword("")
                  go({ q: undefined, language: undefined, channel: undefined, tag: undefined })
                }}
              >
                {t("contacts.clear_filters")}
              </Button>
            )}
            {!search.view && (hasFilters || search.kind) && (
              <Button
                size="compact-xs"
                variant="light"
                leftSection={<IconBookmarkPlus size={14} />}
                onClick={() => setSaveViewOpen(true)}
              >
                {t("contacts.save_view")}
              </Button>
            )}
            {can("contacts:export") && (
              <ExportButton
                title={t("contacts.export")}
                source={{
                  start: () =>
                    tenantApi.contact.exportStart({
                      format: "xlsx",
                      view_id: search.view,
                      kind: search.kind,
                      q: search.q,
                      language: search.language,
                      channel_id: search.channel,
                      tag_id: search.tag,
                      sort: search.sort,
                    }),
                  progress: (id) => (o) => tenantApi.contact.exportProgress(id, o),
                }}
              />
            )}
          </Group>
        }
      />

      {/* 独立搜索与筛选工具条 */}
      <PaneToolbar>
        <Group gap="xs" wrap="wrap">
          <TextInput
            variant="filled"
            style={{ flex: 1, minWidth: 200 }}
            value={keyword}
            onChange={(e) => setKeyword(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape" && keyword) {
                e.stopPropagation()
                setKeyword("")
              }
            }}
            placeholder={t("contacts.search_placeholder")}
            aria-label={t("contacts.search_placeholder")}
            leftSection={<IconSearch size={14} stroke={1.6} />}
            rightSection={keyword ? <CloseButton size="xs" aria-label={t("common.clear_search")} onClick={() => setKeyword("")} /> : null}
          />
          <LanguageSelect
            w={compact ? 120 : 140}
            clearable
            first={activeLanguages}
            placeholder={t("contacts.filter_language")}
            value={search.language ?? null}
            onChange={(v) => go({ language: v || undefined })}
            aria-label={t("contacts.language")}
          />
          {channels.length > 0 && (
            <Select
              w={compact ? 120 : 144}
              clearable
              placeholder={t("contacts.filter_channel")}
              data={channels}
              value={search.channel ?? null}
              onChange={(v) => go({ channel: v || undefined })}
              aria-label={t("contacts.filter_channel")}
            />
          )}
          {tagChoices.length > 0 && (
            <Select
              w={compact ? 116 : 136}
              clearable
              searchable
              placeholder={t("contacts.filter_tag")}
              data={tagChoices}
              value={search.tag ?? null}
              onChange={(v) => go({ tag: v || undefined })}
              aria-label={t("contacts.filter_tag")}
            />
          )}
          <Select
            w={112}
            allowDeselect={false}
            data={sorts}
            value={search.sort}
            onChange={(v) => v && go({ sort: v as ContactSort })}
            aria-label={t("contacts.sort")}
          />
        </Group>
      </PaneToolbar>

      <PaneBody>
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
                  <Skeleton circle h={36} w={36} style={{ flex: "none" }} />
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
          <EmptyState
            title={t(hasFilters ? "contacts.empty_filtered" : "contacts.empty")}
            action={
              hasFilters && (
                <Button
                  size="xs"
                  variant="light"
                  onClick={() => {
                    setKeyword("")
                    go({ q: undefined, language: undefined, channel: undefined, tag: undefined })
                  }}
                >
                  {t("contacts.clear_filters")}
                </Button>
              )
            }
          />
        ) : (
          <>
            {items.map((c) => {
              const displayName = contactDisplayName(c, t)
              const localTime = formatLocalTime(c.time_zone)
              const place = [c.location, localTime ? t("contacts.local_time", { time: localTime }) : ""].filter(Boolean).join(" · ")
              const langLabel = c.language ? languageName(c.language, ui) : ""
              const subline = contactSubline(c)
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
                  onMouseEnter={() => {
                    if (!active) void prefetchContact(queryClient, c.id)
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      openContact(c.id)
                    }
                  }}
                >
                  <Group gap="sm" wrap="nowrap" style={{ flex: narrow ? "1 1 100%" : 2.2, minWidth: 200 }}>
                    <ContactAvatar contact={c} size={36} />
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
                    <IconMessageCircle size={14} style={{ color: "var(--mantine-color-dimmed)" }} />
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
      </PaneBody>
      <ContactViewModal
        opened={saveViewOpen}
        onClose={() => setSaveViewOpen(false)}
        initialConfig={{
          kind: search.kind,
          q: search.q,
          language: search.language,
          channel_id: search.channel,
          tag_id: search.tag,
          sort: search.sort,
        }}
        onSaved={(saved) =>
          go({
            view: saved.id,
            kind: undefined,
            q: undefined,
            language: undefined,
            channel: undefined,
            tag: undefined,
            sort: saved.config.sort ?? "recent",
          })
        }
      />
    </Box>
  )
}

/** contactListQuery 是联系人名录的查询，游标分页（名录与路由 loader 共用）。 */
export function contactListQuery(search: ContactsSearch) {
  return infiniteQueryOptions({
    queryKey: [
      ...kit.queries.contact.key(),
      "cursor-list",
      search.view ?? "",
      search.kind ?? "",
      search.q ?? "",
      search.language ?? "",
      search.channel ?? "",
      search.tag ?? "",
      search.sort,
    ],
    queryFn: ({ pageParam }) =>
      tenantApi.contact.list({
        view_id: search.view,
        kind: search.kind,
        q: search.q,
        language: search.language,
        channel_id: search.channel,
        tag_id: search.tag,
        sort: search.sort,
        cursor: pageParam || undefined,
        per_page: 50,
      }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  })
}
