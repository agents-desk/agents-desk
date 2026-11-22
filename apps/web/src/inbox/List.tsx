import type { ConversationBulkResult, ConversationDTO, ConversationSummary } from "@agents-desk/client/tenant"
import { ActionIcon, Avatar, Badge, Box, Button, Center, Checkbox, ColorSwatch, Group, Indicator, Loader, Menu, SegmentedControl, Select, Skeleton, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { notifications } from "@mantine/notifications"
import { IconArrowsSort, IconCheck, IconFlame, IconLayoutSidebarLeftExpand, IconUserPlus, IconX } from "@tabler/icons-react"
import { notifyError, notifySuccess, useCan, useLanguage, useNavControl, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { flatten, useConversationList, useMe, useMembers, useTags, useTeams, useThresholds } from "./data"
import { useDraft } from "./drafts"
import { type FillWatcher, useFillVisible } from "./fill"
import { activity, duration, waitColor, waitLevel } from "./format"
import { useNow } from "./Nav"
import type { InboxSearch } from "./search"

/** ListProps 是会话列表的设置。 */
interface ListProps {
  search: InboxSearch
  openId?: string // 当前打开的会话
  go: (s: Partial<InboxSearch>) => void
  open: (id: string) => void
}

/**
 * ConversationList 是会话列表：顶部是列表名和积压情况（待回复数、等得最久的时长、打开的会话数），切换状态和排序，
 * 可以进入选择模式批量指派、关闭；下面是会话，滚到底时加载更多。刚有新消息的一行短暂高亮。
 */
export function ConversationList({ search, openId, go, open }: ListProps): ReactNode {
  const t = useT()
  const nav = useNavControl()
  const now = useNow()
  const th = useThresholds()
  const me = useMe()
  const tags = useTags()
  const list = useConversationList(search)
  const tagById = new Map(tags.map((x) => [x.id, x]))
  const inbox = useQuery(kit.queries.conversation.inbox())
  const translation = useQuery(kit.queries.translation.settings())
  const watch = useFillVisible(translation.data?.enabled ?? false, me.data?.language)
  const [selecting, setSelecting] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const items = flatten(list.data?.pages)

  // 列表名和积压情况：来自收件箱的数量
  const team = search.team ? inbox.data?.teams.find((x) => x.id === search.team) : undefined
  const summary: ConversationSummary | undefined = team ?? (inbox.data ? inbox.data[search.box] : undefined)
  const title = team?.name ?? t(`inbox.box_${search.box}` as const)

  // 滚到底时加载下一页
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

  // 刚有新消息的会话：最后一条消息的时间变了，高亮 2 秒
  const seen = useRef(new Map<string, string>())
  const [flash, setFlash] = useState<Set<string>>(new Set())
  useEffect(() => {
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
  }, [items])

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

  const hasToolbar = search.box !== "mentions" || tags.length > 0
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
              <Text fw={600} size="sm" truncate lh={1.25}>
                {title}
              </Text>
              {summary && (
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
              )}
            </Box>
            <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setSelecting(true)} style={{ flex: "none" }}>
              {t("inbox.select")}
            </Button>
          </Group>
          {/* 独立筛选工具条：状态、排序图标菜单与标签筛选，260px 最小栏宽下也不挤压 */}
          {hasToolbar && (
            <Stack gap={6} px="sm" py={8} className="inbox-toolbar">
              {/* @我 的列表不按状态筛选，总是最近被 @ 的在前 */}
              {search.box !== "mentions" && (
                <Group gap={6} wrap="nowrap">
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
                </Group>
              )}
              {tags.length > 0 && (
                <Select
                  size="xs"
                  placeholder={t("inbox.tag_filter")}
                  aria-label={t("inbox.tag_filter")}
                  value={search.tag ?? null}
                  onChange={(v) => go({ tag: v || undefined })}
                  data={tags.map((tag) => ({ value: tag.id, label: tag.name }))}
                  leftSection={
                    search.tag && tagById.get(search.tag) ? (
                      <ColorSwatch color={`var(--mantine-color-${tagById.get(search.tag)?.color}-6)`} size={10} />
                    ) : undefined
                  }
                  renderOption={({ option }) => {
                    const tag = tagById.get(option.value)
                    return (
                      <Group gap="xs" wrap="nowrap">
                        {tag && <ColorSwatch color={`var(--mantine-color-${tag.color}-6)`} size={10} />}
                        <Text size="xs" truncate>
                          {option.label}
                        </Text>
                      </Group>
                    )
                  }}
                  clearable
                  searchable
                  comboboxProps={{ withinPortal: true }}
                />
              )}
            </Stack>
          )}
        </>
      )}
      <Box style={{ flex: 1, overflowY: "auto" }}>
        {list.isPending ? (
          <Stack gap={0}>
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <Box key={i} px="sm" py={10} className="inbox-row">
                <Group gap={10} wrap="nowrap" align="flex-start">
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
              {t(search.tag ? "inbox.empty_filtered" : "inbox.empty")}
            </Text>
            {search.tag && (
              <Button size="xs" variant="light" onClick={() => go({ tag: undefined })}>
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
        {list.isFetchingNextPage && (
          <Center p="sm">
            <Loader size="xs" />
          </Center>
        )}
      </Box>
    </Stack>
  )
}

/**
 * Row 是会话列表的一行：未读点、联系人、渠道、优先级、等待时长（已回复的显示最近活动的时间）；第二行是最后一条消息，
 * 对方的消息是我的语言的译文。还缺译文时第二行先显示原文，这一行出现在页面上时请求补译（watch）。
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
      py={9}
    >
      <Group gap={10} wrap="nowrap" align="flex-start">
        {selecting ? (
          <Checkbox checked={selected} readOnly size="xs" mt={5} tabIndex={-1} />
        ) : (
          <Indicator disabled={!c.unread} color="brand" size={8} offset={2} withBorder style={{ flex: "none" }}>
            <Avatar size={28} name={name} color="initials" />
          </Indicator>
        )}
        <Box style={{ flex: 1, minWidth: 0 }}>
          <Group gap={5} wrap="nowrap">
            <Text size="sm" fw={c.unread ? 700 : 500} truncate>
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
          <Text size="sm" c={draftText || c.unread ? undefined : "dimmed"} truncate mt={2}>
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
                {c.preview}
              </>
            )}
          </Text>
          {c.tags.length > 0 && (
            <Group gap={4} mt={4} wrap="nowrap">
              {c.tags.slice(0, 2).map((tag) => (
                <Badge key={tag.id} size="xs" variant="light" color={tag.color} style={{ flex: "0 1 auto" }}>
                  {tag.name}
                </Badge>
              ))}
              {c.tags.length > 2 && (
                <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                  +{c.tags.length - 2}
                </Badge>
              )}
            </Group>
          )}
        </Box>
      </Group>
    </UnstyledButton>
  )
}

/** contactName 返回会话对方的名字：联系人没有名字时是“访客”。 */
export function contactName(c: ConversationDTO, t: ReturnType<typeof useT>): string {
  return c.contact?.name || t("inbox.visitor")
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
