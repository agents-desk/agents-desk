import type {
  ConversationBulkResult,
  ConversationDTO,
  ConversationSearchHitDTO,
  ConversationSummary,
  ConversationViewConfig,
  ConversationViewSummary,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Center,
  Checkbox,
  CloseButton,
  Group,
  Indicator,
  Loader,
  Menu,
  Popover,
  ScrollArea,
  SegmentedControl,
  Skeleton,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  IconArrowsSort,
  IconBookmarkPlus,
  IconCheck,
  IconFilter,
  IconFlame,
  IconLayoutSidebarLeftExpand,
  IconLock,
  IconPencil,
  IconSearch,
  IconUserPlus,
  IconUsers,
  IconX,
} from "@tabler/icons-react"
import { notifyError, notifySuccess, useCan, useLanguage, useNavControl, useSavedView, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { ContactAvatar, contactDisplayName } from "../contacts/display"
import { flatten, flattenSearchHits, useConversationList, useConversationSearch, useMe, useMembers, useTeams, useThresholds } from "./data"
import { useDraft } from "./drafts"
import { type FillWatcher, useFillVisible } from "./fill"
import { FilterFields, FilterSummaryChips } from "./FilterFields"
import { activity, duration, waitColor, waitLevel } from "./format"
import { HighlightedSnippet } from "./Highlight"
import { useNow } from "./Nav"
import {
  type ConversationFilter,
  countFilterCriteria,
  filterFromSearch,
  type InboxSearch,
  type MatchField,
  normalizeFilter,
  normalizeViewConfig,
  parseFilter,
  type SearchScope,
  serializeFilter,
} from "./search"
import { ViewModal } from "./ViewModal"

/** OpenFocus 是点击搜索结果打开会话时附带的消息定位信息。 */
export interface OpenFocus {
  seq?: number
  matchField?: MatchField
}

/** ListProps 是会话列表的设置。 */
interface ListProps {
  search: InboxSearch
  openId?: string // 当前打开的会话
  go: (s: Partial<InboxSearch>) => void
  open: (id: string, focus?: OpenFocus) => void
}

/**
 * ConversationList 是会话列表：顶部是列表或视图名称和积压情况（待回复数、等得最久的时长、会话数），支持按关键词全文搜索、
 * 切换状态、排序与统一筛选面板（状态、回复状态、优先级、团队、负责人、渠道、标签、语言、自定义属性）。
 * 打开已保存视图时支持直接微调条件实时预览，通过 useSavedView 提示“重置 / 另存为 / 保存更改”；
 * 在普通收件箱添加筛选后可一键“保存为视图”。
 */
export function ConversationList({ search, openId, go, open }: ListProps): ReactNode {
  const t = useT()
  const nav = useNavControl()
  const now = useNow()
  const th = useThresholds()
  const me = useMe()
  const queryClient = useQueryClient()
  const list = useConversationList(search)
  const searchQuery = useConversationSearch(search)
  const inbox = useQuery(kit.queries.conversation.inbox())
  const translation = useQuery(kit.queries.translation.settings())
  const watch = useFillVisible(translation.data?.enabled ?? false, me.data?.language)

  const [selecting, setSelecting] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [draftQ, setDraftQ] = useState(search.q ?? "")
  const [debouncedQ] = useDebouncedValue(draftQ, 250)
  const [filterOpen, setFilterOpen] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [modalEditing, setModalEditing] = useState<ConversationViewSummary | undefined>()
  const [modalInitialConfig, setModalInitialConfig] = useState<ConversationViewConfig | undefined>()

  const searching = Boolean(search.q?.trim())
  const items = flatten(list.data?.pages)
  const hits = flattenSearchHits(searchQuery.data?.pages)
  const searchTotal = searchQuery.data?.pages[0]?.total ?? 0

  // URL 中的 q 被外部改变（例如从命令面板跳转或清空）时同步输入框内容
  const prevUrlQ = useRef(search.q ?? "")
  useEffect(() => {
    const next = search.q ?? ""
    if (next !== prevUrlQ.current) {
      prevUrlQ.current = next
      setDraftQ(next)
    }
  }, [search.q])

  // 输入防抖后同步到 URL 参数，清空时一并清除定位与范围
  const goRef = useRef(go)
  goRef.current = go
  useEffect(() => {
    const trimmed = debouncedQ.trim()
    const current = (search.q ?? "").trim()
    if (trimmed === current) return
    prevUrlQ.current = trimmed
    if (trimmed) {
      goRef.current({ q: trimmed, seq: undefined, matchField: undefined })
    } else {
      goRef.current({ q: undefined, scope: undefined, seq: undefined, matchField: undefined })
    }
  }, [debouncedQ, search.q])

  // 列表/视图名与积压情况：来自收件箱的数量
  const activeView = search.view ? inbox.data?.views.find((v) => v.id === search.view) : undefined
  const viewMissing = Boolean(search.view && inbox.data && !activeView)
  const team = !search.view && search.team ? inbox.data?.teams.find((x) => x.id === search.team) : undefined
  const summary: ConversationSummary | undefined = activeView ?? team ?? (inbox.data && !search.view ? inbox.data[search.box] : undefined)
  const title = activeView?.name ?? team?.name ?? t(`inbox.box_${search.box}` as const)

  // 当前生效的同构筛选条件：视图模式下优先使用临时微调的 search.filter，否则回落到视图已保存配置；
  // 普通模式下优先使用 search.filter（含旧 tag 参数兜底）。
  const parsedUrlFilter = useMemo(() => parseFilter(search.filter), [search.filter])
  const activeFilter: ConversationFilter = useMemo(() => {
    if (activeView) {
      if (parsedUrlFilter) return parsedUrlFilter
      const { sort: _s, ...rest } = normalizeViewConfig(activeView.config)
      return rest
    }
    if (parsedUrlFilter) return parsedUrlFilter
    if (search.tag) return { tag_ids: [search.tag], tag_match: "any" }
    return {}
  }, [activeView, parsedUrlFilter, search.tag])

  const currentViewConfig: ConversationViewConfig = useMemo(
    () => normalizeViewConfig({ ...activeFilter, sort: search.sort }),
    [activeFilter, search.sort],
  )
  const savedViewState = useSavedView(activeView?.config, currentViewConfig, { normalize: normalizeViewConfig })
  const filterCount = countFilterCriteria(activeFilter)
  const showSaveFromPlain = !search.view && (Boolean(parsedUrlFilter) || Boolean(search.tag))

  const updateFilter = (next: ConversationFilter) => {
    const norm = normalizeFilter(next)
    if (activeView) {
      const nextCfg = normalizeViewConfig({ ...norm, sort: search.sort })
      if (savedViewState.same(activeView.config, nextCfg)) {
        go({ filter: undefined, tag: undefined })
      } else {
        go({ filter: JSON.stringify(norm), tag: undefined })
      }
      return
    }
    go({ filter: serializeFilter(norm), tag: undefined })
  }

  const saveViewChanges = useMutation({
    mutationFn: () => {
      if (!activeView) throw new Error("no active view")
      return tenantApi.view.update(activeView.id, { name: activeView.name, config: currentViewConfig })
    },
    onSuccess: () => {
      notifySuccess(t("views.saved"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.view.key() })
      go({ filter: undefined, tag: undefined })
    },
    onError: (err) => notifyError(err),
  })

  const openEditView = () => {
    if (!activeView) return
    setModalEditing(activeView)
    setModalInitialConfig(undefined)
    setModalOpen(true)
  }

  const openSaveAsView = (cfg: ConversationViewConfig) => {
    setModalEditing(undefined)
    setModalInitialConfig(cfg)
    setModalOpen(true)
  }

  // 滚到底时加载下一页（普通列表或搜索结果共用）
  const activeQuery = searching ? searchQuery : list
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = end.current
    if (!el) return
    const io = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting && activeQuery.hasNextPage && !activeQuery.isFetchingNextPage) void activeQuery.fetchNextPage()
    })
    io.observe(el)
    return () => io.disconnect()
  }, [activeQuery])

  // 刚有新消息的会话：最后一条消息的时间变了，高亮 2 秒
  const seen = useRef(new Map<string, string>())
  const [flash, setFlash] = useState<Set<string>>(new Set())
  useEffect(() => {
    if (searching) return
    const changed: string[] = []
    for (const c of items) {
      const before = seen.current.get(c.id)
      if (before && before !== c.last_message_at) changed.push(c.id)
      seen.current.set(c.id, c.last_message_at)
    }
    if (changed.length === 0) return
    setFlash(new Set(changed))
    const timer = setTimeout(() => setFlash(new Set()), 2000)
    return () => clearTimeout(timer)
  }, [items, searching])

  const toggle = (id: string) =>
    setSelected((s) => {
      const next = new Set(s)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  const stopSelecting = useCallback(() => {
    setSelecting(false)
    setSelected(new Set())
  }, [])

  // 选择模式下按 Escape 快捷退出批量操作
  useEffect(() => {
    if (!selecting) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") stopSelecting()
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [selecting, stopSelecting])

  const selectedCount = items.filter((c) => selected.has(c.id)).length
  const allSelected = items.length > 0 && selectedCount === items.length
  const indeterminate = selectedCount > 0 && selectedCount < items.length
  const toggleAll = () => {
    if (allSelected) setSelected(new Set())
    else setSelected(new Set(items.map((c) => c.id)))
  }
  const clearSearch = () => {
    setDraftQ("")
    prevUrlQ.current = ""
    go({ q: undefined, scope: undefined, seq: undefined, matchField: undefined })
  }
  const sortLabel = search.sort === "waiting" ? t("inbox.sort_waiting") : t("inbox.sort_recent")

  return (
    <Stack gap={0} h="100%" className="inbox-list">
      {selecting ? (
        <BulkBar
          ids={[...selected]}
          allSelected={allSelected}
          indeterminate={indeterminate}
          hasItems={items.length > 0}
          onToggleAll={toggleAll}
          onDone={stopSelecting}
        />
      ) : (
        <>
          {/* 52px 基准顶栏：与二级导航头、会话头、详情头严格齐平 */}
          <Group gap="xs" px="sm" wrap="nowrap" className="inbox-header" data-fixed="">
            {nav && !nav.open && (
              <Tooltip label={t("nav.expand_nav")} openDelay={350}>
                <ActionIcon variant="subtle" color="gray" onClick={nav.openNav} aria-label={t("nav.expand_nav")}>
                  <IconLayoutSidebarLeftExpand size={18} stroke={1.6} />
                </ActionIcon>
              </Tooltip>
            )}
            <Box style={{ flex: 1, minWidth: 0 }}>
              <Group gap={5} wrap="nowrap">
                {activeView &&
                  (activeView.scope === "shared" ? (
                    <IconUsers size={14} stroke={1.7} style={{ flex: "none", color: "var(--tn-muted)" }} />
                  ) : (
                    <IconFilter size={14} stroke={1.7} style={{ flex: "none", color: "var(--tn-muted)" }} />
                  ))}
                <Text fw={600} size="sm" truncate lh={1.25}>
                  {title}
                </Text>
              </Group>
              {searching ? (
                <Text size="xs" c="dimmed" truncate lh={1.25} style={{ fontVariantNumeric: "tabular-nums" }}>
                  {searchQuery.isPending ? "…" : t("inbox.search_results_count", { n: searchTotal })}
                </Text>
              ) : (
                summary &&
                !savedViewState.dirty && (
                  <Text size="xs" c="dimmed" truncate lh={1.25} style={{ fontVariantNumeric: "tabular-nums" }}>
                    {t("inbox.backlog_waiting")}{" "}
                    <Text span size="xs" fw={600} c={summary.waiting > 0 ? "deep-orange" : undefined}>
                      {summary.waiting}
                    </Text>
                    {summary.oldest_waiting_since && (
                      <>
                        {" · "}
                        {t("inbox.backlog_oldest")}{" "}
                        <Text span size="xs" fw={600} c={waitColor[waitLevel(summary.oldest_waiting_since, now, th)]}>
                          {duration(summary.oldest_waiting_since, now, t)}
                        </Text>
                      </>
                    )}
                    {" · "}
                    {t("inbox.backlog_open", { n: summary.count })}
                  </Text>
                )
              )}
            </Box>
            {!searching && activeView?.editable && (
              <Tooltip label={t("views.edit")} openDelay={300}>
                <ActionIcon variant="subtle" color="gray" size="sm" aria-label={t("views.edit")} onClick={openEditView}>
                  <IconPencil size={15} />
                </ActionIcon>
              </Tooltip>
            )}
            {!searching && !viewMissing && (
              <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setSelecting(true)} style={{ flex: "none" }}>
                {t("inbox.select")}
              </Button>
            )}
          </Group>

          {/* 独立筛选工具条：搜索框、状态/筛选/排序、摘要胶囊、脏状态保存条，260px 最小栏宽下也不挤压 */}
          {!viewMissing && (
            <Stack gap={6} px="sm" py={8} className="inbox-toolbar">
              <TextInput
                size="xs"
                value={draftQ}
                onChange={(e) => setDraftQ(e.currentTarget.value)}
                onKeyDown={(e) => {
                  if (e.key === "Escape" && draftQ) {
                    e.stopPropagation()
                    clearSearch()
                  }
                }}
                placeholder={t("inbox.search_placeholder")}
                aria-label={t("inbox.search_placeholder")}
                leftSection={<IconSearch size={14} stroke={1.6} />}
                rightSection={
                  draftQ ? (
                    <CloseButton size="xs" aria-label={t("inbox.search_clear")} onClick={clearSearch} />
                  ) : undefined
                }
              />

              {searching ? (
                <SegmentedControl
                  size="xs"
                  fullWidth
                  value={search.scope ?? "all"}
                  onChange={(v) => go({ scope: v as SearchScope })}
                  data={[
                    { value: "all", label: t("inbox.search_scope_all") },
                    { value: "current", label: t("inbox.search_scope_current") },
                  ]}
                />
              ) : (
                <Group gap={6} wrap="nowrap">
                  {!search.view && !parsedUrlFilter && search.box !== "mentions" ? (
                    <SegmentedControl
                      size="xs"
                      style={{ flex: 1, minWidth: 0 }}
                      value={search.status}
                      onChange={(v) => go({ status: v as InboxSearch["status"] })}
                      data={[
                        { value: "open", label: t("inbox.status_open") },
                        { value: "snoozed", label: t("inbox.status_snoozed") },
                        { value: "closed", label: t("inbox.status_closed") },
                      ]}
                    />
                  ) : (
                    <Popover opened={filterOpen} onChange={setFilterOpen} position="bottom-start" width={312} shadow="md" withinPortal>
                      <Popover.Target>
                        <Button
                          size="xs"
                          variant={filterCount > 0 ? "light" : "default"}
                          leftSection={<IconFilter size={14} />}
                          rightSection={
                            filterCount > 0 ? (
                              <Badge size="xs" circle variant="filled">
                                {filterCount}
                              </Badge>
                            ) : undefined
                          }
                          style={{ flex: 1 }}
                          onClick={() => setFilterOpen((o) => !o)}
                        >
                          {t("views.filter_button")}
                        </Button>
                      </Popover.Target>
                      <Popover.Dropdown p="sm">
                        <ScrollArea.Autosize mah={420} offsetScrollbars="y">
                          <FilterFields value={activeFilter} onChange={updateFilter} compact />
                        </ScrollArea.Autosize>
                        {filterCount > 0 && (
                          <Group justify="flex-end" mt="xs" pt="xs" style={{ borderTop: "1px solid var(--tn-border)" }}>
                            <Button size="compact-xs" variant="subtle" color="gray" onClick={() => updateFilter({})}>
                              {t("views.clear_filters")}
                            </Button>
                          </Group>
                        )}
                      </Popover.Dropdown>
                    </Popover>
                  )}

                  {!search.view && !parsedUrlFilter && search.box !== "mentions" && (
                    <Popover opened={filterOpen} onChange={setFilterOpen} position="bottom-end" width={312} shadow="md" withinPortal>
                      <Popover.Target>
                        <Tooltip label={t("views.filter_button")} openDelay={300}>
                          <Indicator disabled={filterCount === 0} size={7} offset={3}>
                            <ActionIcon
                              variant={filterCount > 0 ? "light" : "default"}
                              size={30}
                              aria-label={t("views.filter_button")}
                              style={{ flex: "none" }}
                              onClick={() => setFilterOpen((o) => !o)}
                            >
                              <IconFilter size={15} />
                            </ActionIcon>
                          </Indicator>
                        </Tooltip>
                      </Popover.Target>
                      <Popover.Dropdown p="sm">
                        <ScrollArea.Autosize mah={420} offsetScrollbars="y">
                          <FilterFields
                            value={parsedUrlFilter ? activeFilter : filterFromSearch(search)}
                            onChange={(next) => updateFilter(next)}
                            compact
                          />
                        </ScrollArea.Autosize>
                      </Popover.Dropdown>
                    </Popover>
                  )}

                  {(search.view || search.box !== "mentions") && (
                    <Menu position="bottom-end" withinPortal>
                      <Menu.Target>
                        <Tooltip label={`${t("inbox.sort")} · ${sortLabel}`} openDelay={300}>
                          <ActionIcon variant="default" size={30} aria-label={t("inbox.sort")} style={{ flex: "none" }}>
                            <IconArrowsSort size={15} />
                          </ActionIcon>
                        </Tooltip>
                      </Menu.Target>
                      <Menu.Dropdown>
                        <Menu.Label>{t("inbox.sort")}</Menu.Label>
                        {(["waiting", "recent"] as const).map((mode) => (
                          <Menu.Item
                            key={mode}
                            rightSection={search.sort === mode ? <IconCheck size={14} color="var(--mantine-primary-color-filled)" /> : undefined}
                            onClick={() => go({ sort: mode })}
                          >
                            {t(mode === "waiting" ? "inbox.sort_waiting" : "inbox.sort_recent")}
                          </Menu.Item>
                        ))}
                      </Menu.Dropdown>
                    </Menu>
                  )}
                </Group>
              )}

              {!searching && <FilterSummaryChips filter={activeFilter} onChange={updateFilter} />}

              {!searching && activeView && savedViewState.dirty && (
                <Group gap={6} justify="space-between" px={8} py={5} className="inbox-view-dirty" wrap="wrap">
                  <Text size="xs" fw={500} c="deep-orange">
                    {t("views.dirty_hint")}
                  </Text>
                  <Group gap={4} wrap="nowrap">
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      onClick={() =>
                        go({
                          filter: undefined,
                          tag: undefined,
                          sort: activeView.config.sort ?? "waiting",
                        })
                      }
                    >
                      {t("views.reset")}
                    </Button>
                    <Button size="compact-xs" variant="light" onClick={() => openSaveAsView(currentViewConfig)}>
                      {t(activeView.editable ? "views.save_as" : "views.save_as_mine")}
                    </Button>
                    {activeView.editable && (
                      <Button size="compact-xs" loading={saveViewChanges.isPending} onClick={() => saveViewChanges.mutate()}>
                        {t("views.save_changes")}
                      </Button>
                    )}
                  </Group>
                </Group>
              )}

              {!searching && showSaveFromPlain && (
                <Group justify="space-between" gap={6} wrap="nowrap">
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="gray"
                    onClick={() => go({ filter: undefined, tag: undefined })}
                  >
                    {t("views.clear_filters")}
                  </Button>
                  <Button
                    size="compact-xs"
                    variant="light"
                    leftSection={<IconBookmarkPlus size={13} />}
                    onClick={() => openSaveAsView(filterFromSearch(search))}
                  >
                    {t("views.save_as")}
                  </Button>
                </Group>
              )}
            </Stack>
          )}
        </>
      )}

      <Box style={{ flex: 1, overflowY: "auto" }}>
        {viewMissing ? (
          <Stack gap="sm" align="center" p="xl">
            <Text c="dimmed" size="sm" ta="center">
              {t("views.not_found")}
            </Text>
            <Button size="xs" variant="light" onClick={() => go({ view: undefined, filter: undefined, box: "mine", team: undefined })}>
              {t("views.back_to_mine")}
            </Button>
          </Stack>
        ) : searching ? (
          searchQuery.isPending ? (
            <Stack gap={0}>
              {[0, 1, 2, 3, 4].map((i) => (
                <Box key={i} px="sm" py={10} className="inbox-row" data-search="">
                  <Group justify="space-between" mb={6}>
                    <Skeleton h={12} w="44%" radius="sm" />
                    <Skeleton h={10} w={36} radius="sm" />
                  </Group>
                  <Skeleton h={11} w="88%" radius="sm" />
                </Box>
              ))}
            </Stack>
          ) : hits.length === 0 ? (
            <Text c="dimmed" size="sm" ta="center" p="xl">
              {t("inbox.search_empty", { q: search.q?.trim() ?? "" })}
            </Text>
          ) : (
            hits.map((hit) => (
              <SearchRow
                key={`${hit.conversation.id}:${hit.match.item_id}`}
                hit={hit}
                active={hit.conversation.id === openId}
                onClick={() =>
                  open(hit.conversation.id, {
                    seq: hit.match.seq,
                    matchField: hit.match.field as MatchField,
                  })
                }
              />
            ))
          )
        ) : list.isPending ? (
          <Stack gap={0}>
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <Box key={i} px="sm" className="inbox-row">
                <Group gap={10} wrap="nowrap" align="center" w="100%">
                  <Skeleton circle h={28} w={28} style={{ flex: "none" }} />
                  <Box style={{ flex: 1, minWidth: 0 }}>
                    <Group justify="space-between" mb={6}>
                      <Skeleton h={12} w="46%" radius="sm" />
                      <Skeleton h={10} w={36} radius="sm" />
                    </Group>
                    <Skeleton h={11} w="84%" radius="sm" />
                  </Box>
                </Group>
              </Box>
            ))}
          </Stack>
        ) : items.length === 0 ? (
          <Stack gap="xs" align="center" p="xl">
            <Text c="dimmed" size="sm" ta="center">
              {t(filterCount > 0 ? "inbox.empty_filtered" : "inbox.empty")}
            </Text>
            {filterCount > 0 && !search.view && (
              <Button size="xs" variant="light" onClick={() => go({ filter: undefined, tag: undefined })}>
                {t("inbox.clear_filters")}
              </Button>
            )}
          </Stack>
        ) : (
          items.map((c) => (
            <Row
              key={c.id}
              c={c}
              now={now}
              active={c.id === openId}
              flash={flash.has(c.id)}
              selecting={selecting}
              selected={selected.has(c.id)}
              onClick={() => (selecting ? toggle(c.id) : open(c.id))}
              watch={watch}
            />
          ))
        )}
        <div ref={end} />
        {activeQuery.isFetchingNextPage && (
          <Center p="sm">
            <Loader size="xs" />
          </Center>
        )}
      </Box>

      <ViewModal
        opened={modalOpen}
        onClose={() => setModalOpen(false)}
        editing={modalEditing}
        initialConfig={modalInitialConfig}
        onSaved={(saved) =>
          go({
            box: "all",
            team: undefined,
            view: saved.id,
            filter: undefined,
            tag: undefined,
            sort: saved.config.sort ?? "waiting",
          })
        }
      />
    </Stack>
  )
}

/** statusColor 是会话状态的语义徽标颜色。 */
const statusColor = { open: "bright-blue", snoozed: "yellow", closed: "gray" } as const

/** SearchRow 是全文搜索结果中的一行会话：联系人、渠道、状态、命中时间、高亮摘要以及同会话命中条数。 */
function SearchRow({ hit, active, onClick }: { hit: ConversationSearchHitDTO; active: boolean; onClick: () => void }): ReactNode {
  const t = useT()
  const language = useLanguage()
  const zone = useTimeZone()
  const c = hit.conversation
  const m = hit.match
  return (
    <UnstyledButton onClick={onClick} className="inbox-row" data-search="" data-active={active || undefined} w="100%" px="sm" py={9}>
      <Box style={{ minWidth: 0 }}>
        <Group gap={6} wrap="nowrap">
          <Text size="sm" fw={600} truncate>
            {contactName(c, t)}
          </Text>
          {c.channel && (
            <Badge size="xs" variant="outline" color="gray" style={{ flex: "none" }}>
              {c.channel.name}
            </Badge>
          )}
          <Badge size="xs" variant="light" color={statusColor[c.status]} style={{ flex: "none" }}>
            {t(`inbox.status_${c.status}` as const)}
          </Badge>
          <Text size="xs" c="dimmed" ml="auto" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
            {activity(m.created_at, language, t, zone)}
          </Text>
        </Group>
        <Text size="xs" c="dimmed" lineClamp={2} mt={4} lh={1.45}>
          {m.kind === "note" && (
            <Badge
              size="xs"
              variant="light"
              color="yellow"
              leftSection={<IconLock size={10} />}
              mr={5}
              style={{ verticalAlign: "middle" }}
            >
              {t("inbox.search_note_tag")}
            </Badge>
          )}
          {m.kind === "message" && m.field === "original" && m.actor.type === "contact" && (
            <Badge size="xs" variant="light" color="gray" mr={5} style={{ verticalAlign: "middle" }}>
              {t("inbox.search_original_tag")}
            </Badge>
          )}
          <HighlightedSnippet parts={m.snippet} />
        </Text>
        {hit.match_count > 1 && (
          <Text size="xs" c="brand" mt={3}>
            {t("inbox.search_match_count", { n: hit.match_count })}
          </Text>
        )}
      </Box>
    </UnstyledButton>
  )
}

/**
 * Row 是会话列表的一行（固定 60px 等高两行结构）：
 * 第一行是头像/未读角标、联系人、渠道、优先级、等待时长（已回复的显示最近活动时间）；
 * 第二行左侧是草稿或最后一条消息预览，右侧紧凑展示首个标签与 +N 计数（悬停提示全部标签）。
 * 还缺译文时第二行先显示原文，这一行出现在页面上时请求补译（watch）。
 */
function Row({
  c,
  now,
  active,
  flash,
  selecting,
  selected,
  onClick,
  watch,
}: {
  c: ConversationDTO
  now: number
  active: boolean
  flash: boolean
  selecting: boolean
  selected: boolean
  onClick: () => void
  watch: FillWatcher
}): ReactNode {
  const t = useT()
  const language = useLanguage()
  const zone = useTimeZone()
  const th = useThresholds()
  const me = useMe()
  const { byId } = useMembers()
  const draft = useDraft(c.id)
  const draftText = !active && draft?.text.trim() ? draft.text.trim() : ""
  const level = c.waiting_since ? waitLevel(c.waiting_since, now, th) : "ok"
  const alertWait = Boolean(c.waiting_since) && level !== "ok"
  const who =
    c.preview_actor?.type === "member"
      ? c.preview_actor.id === me.data?.id
        ? t("inbox.you")
        : (byId.get(c.preview_actor.id)?.name ?? "")
      : ""
  const name = contactName(c, t)
  const firstTag = c.tags[0]
  return (
    <UnstyledButton
      onClick={onClick}
      ref={c.preview_untranslated && c.preview_item_id ? watch(c.preview_item_id) : undefined}
      className="inbox-row"
      data-active={active || undefined}
      data-flash={flash || undefined}
      data-read={!c.unread || undefined}
      w="100%"
      px="sm"
    >
      <Group gap={10} wrap="nowrap" align="center" w="100%" style={{ minWidth: 0 }}>
        {selecting ? (
          <Checkbox checked={selected} readOnly size="xs" tabIndex={-1} style={{ flex: "none" }} />
        ) : (
          <Indicator disabled={!c.unread} color="brand" size={8} offset={2} withBorder style={{ flex: "none" }}>
            <ContactAvatar contact={c.contact} size={28} />
          </Indicator>
        )}
        <Box style={{ flex: 1, minWidth: 0 }}>
          <Group gap={5} wrap="nowrap" h={20}>
            <Text size="sm" fw={c.unread ? 700 : 500} truncate lh={1.3}>
              {name}
            </Text>
            {c.channel && (
              <Badge size="xs" variant="outline" color="gray" style={{ flex: "none" }}>
                {c.channel.name}
              </Badge>
            )}
            {c.priority === "high" && (
              <Badge size="xs" variant="light" color="red" leftSection={<IconFlame size={10} />} style={{ flex: "none" }}>
                {t("inbox.priority_high")}
              </Badge>
            )}
            <Text
              size="xs"
              ml="auto"
              px={alertWait ? 6 : 0}
              className="inbox-wait"
              data-level={alertWait ? level : undefined}
              style={{ flex: "none" }}
              c={alertWait ? undefined : "dimmed"}
              fw={alertWait ? 600 : undefined}
            >
              {c.waiting_since ? duration(c.waiting_since, now, t) : activity(c.last_message_at, language, t, zone)}
            </Text>
          </Group>
          <Group gap={6} wrap="nowrap" align="center" mt={3} h={20}>
            <Text size="sm" c={draftText || c.unread ? undefined : "dimmed"} truncate lh={1.3} style={{ flex: 1, minWidth: 0 }}>
              {draftText ? (
                <>
                  <Text span size="sm" fw={600} c="deep-orange">
                    {t("inbox.draft_prefix")}
                  </Text>
                  {draftText}
                </>
              ) : (
                <>
                  {who && (
                    <Text span size="sm" c="dimmed">
                      {t("inbox.said", { who })}
                    </Text>
                  )}
                  {c.preview || "\u00A0"}
                </>
              )}
            </Text>
            {firstTag && (
              <Tooltip label={c.tags.map((x) => x.name).join(" · ")} openDelay={250}>
                <Group gap={3} wrap="nowrap" style={{ flex: "none" }}>
                  <Badge size="xs" variant="light" color={firstTag.color} maw={76}>
                    {firstTag.name}
                  </Badge>
                  {c.tags.length > 1 && (
                    <Badge size="xs" variant="light" color="gray" px={5} style={{ fontVariantNumeric: "tabular-nums" }}>
                      +{c.tags.length - 1}
                    </Badge>
                  )}
                </Group>
              </Tooltip>
            )}
          </Group>
        </Box>
      </Group>
    </UnstyledButton>
  )
}

/** contactName 返回会话对方的展示名：真实姓名优先，其次邮箱，匿名访客显示“访客 #编号”。 */
export function contactName(c: ConversationDTO, t: ReturnType<typeof useT>): string {
  return contactDisplayName(c.contact, t)
}

/** BulkBar 是选择模式顶部的批量操作条：支持全选当前列表、指派、关闭与退出（Esc），在 260px 窄栏下不溢出。 */
function BulkBar({
  ids,
  allSelected,
  indeterminate,
  hasItems,
  onToggleAll,
  onDone,
}: {
  ids: string[]
  allSelected: boolean
  indeterminate: boolean
  hasItems: boolean
  onToggleAll: () => void
  onDone: () => void
}): ReactNode {
  const t = useT()
  const can = useCan()
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()
  const teams = useTeams().filter((x) => x.status === "active")
  // done 提示做成了几个；有失败的（例如没有权限、会话已不存在）时另外提示第一条原因
  const done = (results: ConversationBulkResult[], message: "inbox.bulk_assigned" | "inbox.bulk_closed") => {
    const ok = results.filter((r) => r.ok).length
    notifySuccess(t(message, { n: ok }))
    const failed = results.find((r) => !r.ok)
    if (failed) notifications.show({ color: "yellow", message: t("inbox.bulk_failed", { n: results.length - ok, reason: failed.message ?? "" }) })
    void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
    onDone()
  }
  const assign = useMutation({
    mutationFn: (body: { assignee_id?: string; team_id?: string }) => tenantApi.conversation.bulkAssign({ ids, ...body }),
    onSuccess: (r) => done(r, "inbox.bulk_assigned"),
    onError: (err) => notifyError(err),
  })
  const close = useMutation({
    mutationFn: () => tenantApi.conversation.bulkClose({ ids }),
    onSuccess: (r) => done(r, "inbox.bulk_closed"),
    onError: (err) => notifyError(err),
  })
  // 没有“改派给别人”的权限时只能领取
  const others = can("conversations:assign")
  return (
    <Group gap={6} px="sm" bg="var(--tn-active-bg)" wrap="nowrap" className="inbox-header" data-fixed="">
      <Tooltip label={t("inbox.select_all")} openDelay={300}>
        <Checkbox
          size="xs"
          checked={allSelected}
          indeterminate={indeterminate}
          disabled={!hasItems}
          onChange={onToggleAll}
          aria-label={t("inbox.select_all")}
          style={{ flex: "none" }}
        />
      </Tooltip>
      <Text size="sm" fw={600} truncate style={{ flex: 1, minWidth: 0, fontVariantNumeric: "tabular-nums" }}>
        {t("inbox.selected", { n: ids.length })}
      </Text>
      <Group gap={4} wrap="nowrap" style={{ flex: "none" }}>
        <Menu position="bottom-end" withinPortal>
          <Menu.Target>
            <Tooltip label={t("inbox.assign")} openDelay={250}>
              <ActionIcon variant="default" size={28} disabled={ids.length === 0} loading={assign.isPending} aria-label={t("inbox.assign")}>
                <IconUserPlus size={14} />
              </ActionIcon>
            </Tooltip>
          </Menu.Target>
          <Menu.Dropdown mah={360} style={{ overflowY: "auto" }}>
            {me.data && <Menu.Item onClick={() => assign.mutate({ assignee_id: me.data.id })}>{t("inbox.assign_me")}</Menu.Item>}
            {others && (
              <>
                <Menu.Label>{t("inbox.assign_members")}</Menu.Label>
                {members
                  .filter((m) => m.id !== me.data?.id)
                  .map((m) => (
                    <Menu.Item key={m.id} onClick={() => assign.mutate({ assignee_id: m.id })}>
                      {m.name}
                    </Menu.Item>
                  ))}
                <Menu.Label>{t("inbox.assign_teams")}</Menu.Label>
                {teams.map((team) => (
                  <Menu.Item key={team.id} onClick={() => assign.mutate({ team_id: team.id })}>
                    {team.name}
                  </Menu.Item>
                ))}
              </>
            )}
          </Menu.Dropdown>
        </Menu>
        <Tooltip label={t("inbox.close")} openDelay={250}>
          <ActionIcon variant="default" size={28} disabled={ids.length === 0} loading={close.isPending} onClick={() => close.mutate()} aria-label={t("inbox.close")}>
            <IconCheck size={14} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label={t("inbox.cancel")} openDelay={250}>
          <ActionIcon variant="subtle" color="gray" size={28} onClick={onDone} aria-label={t("inbox.cancel")}>
            <IconX size={15} />
          </ActionIcon>
        </Tooltip>
      </Group>
    </Group>
  )
}
