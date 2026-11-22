import type { ConversationBulkResult, ConversationDTO, ConversationSummary } from "@agents-desk/client/tenant"
import { ActionIcon, Badge, Box, Button, Center, Checkbox, ColorSwatch, Group, Loader, Menu, SegmentedControl, Select, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { notifications } from "@mantine/notifications"
import { IconChevronDown, IconLayoutSidebarLeftExpand } from "@tabler/icons-react"
import { notifyError, notifySuccess, useCan, useLanguage, useNavControl, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useEffect, useRef, useState } from "react"
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
  const stopSelecting = () => {
    setSelecting(false)
    setSelected(new Set())
  }

  const hasToolbar = search.box !== "mentions" || tags.length > 0

  return (
    <Stack gap={0} h="100%" className="inbox-list">
      {selecting ? (
        <BulkBar ids={[...selected]} onDone={stopSelecting} />
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
                <Text size="xs" c="dimmed" truncate lh={1.25}>
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
          {/* 独立筛选工具条：状态、排序与标签筛选完整保留，视觉上与顶栏分层 */}
          {hasToolbar && (
            <Stack gap={6} px="sm" py={8} className="inbox-toolbar">
              {/* @我 的列表不按状态筛选，总是最近被 @ 的在前 */}
              {search.box !== "mentions" && (
                <Group gap={6} wrap="nowrap">
                  <SegmentedControl
                    size="xs"
                    style={{ flex: 1 }}
                    value={search.status}
                    onChange={(v) => go({ status: v as InboxSearch["status"] })}
                    data={[
                      { value: "open", label: t("inbox.status_open") },
                      { value: "snoozed", label: t("inbox.status_snoozed") },
                      { value: "closed", label: t("inbox.status_closed") },
                    ]}
                  />
                  <Select
                    size="xs"
                    w={112}
                    allowDeselect={false}
                    value={search.sort}
                    onChange={(v) => v && go({ sort: v as InboxSearch["sort"] })}
                    data={[
                      { value: "waiting", label: t("inbox.sort_waiting") },
                      { value: "recent", label: t("inbox.sort_recent") },
                    ]}
                    aria-label={t("inbox.sort")}
                    comboboxProps={{ withinPortal: true }}
                  />
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
          <Center p="xl">
            <Loader size="sm" />
          </Center>
        ) : items.length === 0 ? (
          <Text c="dimmed" size="sm" ta="center" p="xl">
            {t("inbox.empty")}
          </Text>
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
      <Group gap={8} wrap="nowrap" align="flex-start">
        {selecting && <Checkbox checked={selected} readOnly size="xs" mt={3} tabIndex={-1} />}
        <Box style={{ flex: 1, minWidth: 0 }}>
          <Group gap={6} wrap="nowrap">
            <Box w={7} h={7} style={{ borderRadius: "50%", flex: "none" }} bg={c.unread ? "brand" : "transparent"} />
            <Text size="sm" fw={c.unread ? 700 : 500} truncate>
              {contactName(c, t)}
            </Text>
            {c.channel && (
              <Badge size="xs" variant="outline" color="gray" style={{ flex: "none" }}>
                {c.channel.name}
              </Badge>
            )}
            {c.priority === "high" && (
              <Text size="xs" c="red" fw={600} style={{ flex: "none" }}>
                {t("inbox.priority_high")}
              </Text>
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
          <Text size="sm" c={draftText || c.unread ? undefined : "dimmed"} truncate mt={2} pl={13}>
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
            <Group gap={4} mt={4} pl={13} wrap="nowrap">
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

/** BulkBar 是选择模式顶部的批量操作条：已选数量、指派、关闭、取消。完成后提示结果并退出选择模式。 */
function BulkBar({ ids, onDone }: { ids: string[]; onDone: () => void }): ReactNode {
  const t = useT()
  const can = useCan()
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()
  const teams = useTeams()
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
      <Text size="sm" fw={600}>
        {t("inbox.selected", { n: ids.length })}
      </Text>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          <Button size="xs" variant="default" ml="auto" disabled={ids.length === 0} rightSection={<IconChevronDown size={14} />} loading={assign.isPending}>
            {t("inbox.assign")}
          </Button>
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
      <Button size="xs" variant="default" disabled={ids.length === 0} loading={close.isPending} onClick={() => close.mutate()}>
        {t("inbox.close")}
      </Button>
      <Button size="xs" variant="subtle" color="gray" onClick={onDone}>
        {t("inbox.cancel")}
      </Button>
    </Group>
  )
}
