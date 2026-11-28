import type { ConversationTicketDTO } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Center,
  CloseButton,
  Group,
  Loader,
  Select,
  Skeleton,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { IconFlame, IconLink, IconLock, IconPlus, IconSearch } from "@tabler/icons-react"
import { EmptyState, PaneBody, PaneHeader, PaneToolbar, StatusDot, useCan, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { kit } from "../api"
import { useMe, useMembers, useTeams } from "../inbox/data"
import { useDraft } from "../inbox/drafts"
import { activity } from "../inbox/format"
import { flattenTickets, prefetchTicket, TICKET_STATUSES, ticketStatusColor, ticketStatusLabel, useTicketList, useTicketTypes } from "./meta"
import type { TicketSearch, TicketStatusFilter } from "./search"
import { useListKeys } from "../layout"

/** TicketListProps 是工单列表栏的设置。 */
interface TicketListProps {
  search: TicketSearch
  openId?: string
  go: (patch: Partial<TicketSearch>) => void
  open: (id: string) => void
  onCreate: () => void
}

/**
 * TicketList 是收件箱二级导航“工单”分组对应的工单列表栏：
 * 顶部展示当前分组名称、未解决计数和“新建工单”入口；工具条支持按 #编号/标题搜索、状态筛选与工单类型筛选；
 * 列表按游标无限滚动，每行显示 #编号、状态、高优标记、标题、类型、关联会话数和最近活动时间。
 */
export function TicketList({ search, openId, go, open, onCreate }: TicketListProps): ReactNode {
  const t = useT()
  const can = useCan()
  const inbox = useQuery(kit.queries.conversation.inbox())
  const list = useTicketList(search)
  const types = useTicketTypes(false)
  const items = flattenTickets(list.data?.pages)
  // 列表的顺序按 ID 串比较，数据刷新但顺序不变时不重新登记快捷键
  const idsKey = items.map((x) => x.id).join("\n")
  const ids = useMemo(() => (idsKey ? idsKey.split("\n") : []), [idsKey])
  const queryClient = useQueryClient()
  const warm = useCallback((id: string) => void prefetchTicket(queryClient, id), [queryClient])
  useListKeys(ids, openId, open, warm)

  const [draftQ, setDraftQ] = useState(search.q ?? "")
  const [debouncedQ] = useDebouncedValue(draftQ, 250)

  const prevUrlQ = useRef(search.q ?? "")
  useEffect(() => {
    const next = search.q ?? ""
    if (next !== prevUrlQ.current) {
      prevUrlQ.current = next
      setDraftQ(next)
    }
  }, [search.q])

  const goRef = useRef(go)
  goRef.current = go
  useEffect(() => {
    const trimmed = debouncedQ.trim()
    const current = (search.q ?? "").trim()
    if (trimmed === current) return
    prevUrlQ.current = trimmed
    goRef.current({ q: trimmed || undefined })
  }, [debouncedQ, search.q])

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

  const boxTitle = t(`tickets.box_${search.box}` as const)
  const unresolvedCount = inbox.data?.tickets[search.box]
  const hasFilters = Boolean(search.q?.trim() || search.type || search.status !== "unresolved")

  const statusOptions = [
    { value: "unresolved", label: t("tickets.status_unresolved") },
    { value: "all", label: t("tickets.status_all") },
    ...TICKET_STATUSES.map((st) => ({ value: st, label: ticketStatusLabel(st, t) })),
  ]
  const typeOptions = [{ value: "", label: t("tickets.filter_type_all") }, ...types.map((x) => ({ value: x.id, label: x.name }))]

  return (
    <Stack gap={0} h="100%">
      <PaneHeader
        showNavToggle
        title={
          <Text fw={600} size="sm" truncate lh={1.25}>
            {boxTitle}
          </Text>
        }
        subtitle={
          unresolvedCount !== undefined ? (
            <Text size="xs" c="dimmed" truncate lh={1.25} style={{ fontVariantNumeric: "tabular-nums" }}>
              {t("tickets.count_unresolved", { n: unresolvedCount })}
            </Text>
          ) : undefined
        }
        actions={
          can("conversations:reply") ? (
            <Tooltip label={t("tickets.create")} openDelay={300}>
              <ActionIcon variant="light" size="sm" aria-label={t("tickets.create")} onClick={onCreate}>
                <IconPlus size={16} />
              </ActionIcon>
            </Tooltip>
          ) : undefined
        }
      />

      <PaneToolbar gap={6}>
        <TextInput
          variant="filled"
          value={draftQ}
          onChange={(e) => setDraftQ(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape" && draftQ) {
              e.stopPropagation()
              setDraftQ("")
              prevUrlQ.current = ""
              go({ q: undefined })
            }
          }}
          placeholder={t("tickets.search_placeholder")}
          aria-label={t("tickets.search_placeholder")}
          leftSection={<IconSearch size={14} stroke={1.6} />}
          rightSection={
            draftQ ? (
              <CloseButton
                size="xs"
                aria-label={t("common.clear_search")}
                onClick={() => {
                  setDraftQ("")
                  prevUrlQ.current = ""
                  go({ q: undefined })
                }}
              />
            ) : undefined
          }
        />

        <Group gap={6} grow wrap="nowrap">
          <Select
            value={search.status}
            onChange={(v) => v && go({ status: v as TicketStatusFilter })}
            data={statusOptions}
            allowDeselect={false}
            aria-label={t("tickets.status")}
          />
          <Select
            value={search.type ?? ""}
            onChange={(v) => go({ type: v || undefined })}
            data={typeOptions}
            allowDeselect={false}
            aria-label={t("tickets.type")}
          />
        </Group>
      </PaneToolbar>

      <PaneBody>
        {list.isPending ? (
          <Stack gap={0}>
            {[0, 1, 2, 3, 4].map((i) => (
              <Box key={i} px="var(--tn-pane-px)" py={10} className="inbox-row" data-search="">
                <Group justify="space-between" mb={6}>
                  <Skeleton h={12} w="56%" radius="sm" />
                  <Skeleton h={10} w={36} radius="sm" />
                </Group>
                <Skeleton h={11} w="78%" radius="sm" />
              </Box>
            ))}
          </Stack>
        ) : list.isError ? (
          <EmptyState
            title={t("common.load_failed")}
            action={
              <Button size="xs" variant="light" onClick={() => void list.refetch()}>
                {t("common.retry")}
              </Button>
            }
          />
        ) : items.length === 0 ? (
          <EmptyState
            title={t(hasFilters ? "tickets.empty_filtered" : "tickets.empty")}
            action={
              hasFilters && (
                <Button size="xs" variant="light" onClick={() => go({ status: "unresolved", type: undefined, q: undefined })}>
                  {t("tickets.clear_filters")}
                </Button>
              )
            }
          />
        ) : (
          items.map((tk) => <TicketRow key={tk.id} tk={tk} active={tk.id === openId} onClick={() => open(tk.id)} />)
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

/** TicketRow 是工单列表中的单行卡片：第一行展示编号、标题、高优和最近时间，第二行展示状态、类型、归属和关联会话数。 */
function TicketRow({ tk, active, onClick }: { tk: ConversationTicketDTO; active: boolean; onClick: () => void }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()

  const assigneeName =
    tk.assignee?.type === "member" ? (tk.assignee.id === me.data?.id ? t("inbox.you") : byId.get(tk.assignee.id)?.name) : undefined
  const teamName = tk.team_id ? teams.find((x) => x.id === tk.team_id)?.name : undefined
  const ownerText = assigneeName ?? teamName
  const draft = useDraft(tk.id)
  const draftText = !active && draft?.text.trim() ? draft.text.trim() : ""
  const queryClient = useQueryClient()
  // 悬停时预取工单和时间线，点下去时直接显示
  const warm = () => {
    if (!active) void prefetchTicket(queryClient, tk.id)
  }

  return (
    <UnstyledButton
      onClick={onClick}
      onMouseEnter={warm}
      onFocus={warm}
      className="inbox-row"
      data-search=""
      data-active={active || undefined}
      data-unread={tk.unread || undefined}
      data-read={!tk.unread || undefined}
      w="100%"
      px="var(--tn-pane-px)"
      py={10}
    >
      <Stack gap={5} style={{ minWidth: 0 }}>
        <Group gap={5} wrap="nowrap">
          {tk.unread && <StatusDot color="red" />}
          <Text size="xs" fw={tk.unread ? 600 : 400} c={tk.unread ? undefined : "dimmed"} style={{ fontVariantNumeric: "tabular-nums" }}>
            #{tk.number}
          </Text>
          <Text size="sm" fw={tk.unread ? 700 : 400} className="inbox-row-title" truncate style={{ flex: 1, minWidth: 0 }}>
            {tk.title_translated || tk.title}
          </Text>
          {tk.visibility === "members" && (
            <Tooltip label={t("tickets.visibility_members")} openDelay={300}>
              <IconLock size={12} color="var(--tn-muted)" style={{ flex: "none" }} />
            </Tooltip>
          )}
          {tk.priority === "high" && (
            <Tooltip label={t("inbox.priority_high")} openDelay={300}>
              <span className="inbox-priority-icon" aria-label={t("inbox.priority_high")}>
                <IconFlame size={14} stroke={1.6} />
              </span>
            </Tooltip>
          )}
          <Text
            size="xs"
            c={tk.unread ? undefined : "dimmed"}
            fw={tk.unread ? 600 : undefined}
            style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}
          >
            {activity(tk.last_message_at, ui, t, zone)}
          </Text>
        </Group>

        <Group gap={6} wrap="nowrap">
          <Badge size="xs" variant="light" color={ticketStatusColor[tk.status]} style={{ flex: "none" }}>
            {ticketStatusLabel(tk.status, t)}
          </Badge>
          {draftText ? (
            <Text size="xs" truncate style={{ flex: 1, minWidth: 0 }}>
              <Text span size="xs" fw={600} c="deep-orange">
                {t("inbox.draft_prefix")}
              </Text>
              {draftText}
            </Text>
          ) : (
            <>
              {tk.type_name && (
                <Text size="xs" c="dimmed" truncate style={{ minWidth: 0 }}>
                  {tk.type_name}
                </Text>
              )}
              {ownerText && (
                <>
                  {tk.type_name && (
                    <Text size="xs" c="dimmed" style={{ flex: "none" }}>
                      ·
                    </Text>
                  )}
                  <Text size="xs" c="dimmed" truncate style={{ minWidth: 0 }}>
                    {ownerText}
                  </Text>
                </>
              )}
            </>
          )}
          {tk.linked_count > 0 && (
            <Group gap={3} wrap="nowrap" ml="auto" style={{ flex: "none" }}>
              <IconLink size={12} color="var(--tn-muted)" />
              <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                {tk.linked_count}
              </Text>
            </Group>
          )}
        </Group>
      </Stack>
    </UnstyledButton>
  )
}
