import type { ConversationItemDTO, ConversationTicketDTO, TicketStatus } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Avatar,
  Badge,
  Box,
  Button,
  Center,
  CopyButton,
  Divider,
  Drawer,
  Group,
  Loader,
  Menu,
  Paper,
  SegmentedControl,
  Skeleton,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { useElementSize, useMediaQuery } from "@mantine/hooks"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  IconCheck,
  IconChevronDown,
  IconCopy,
  IconLanguage,
  IconLayoutSidebarRight,
  IconLock,
  IconPencil,
  IconUserPlus,
  IconWorld,
} from "@tabler/icons-react"
import {
  ImageLightbox,
  notifyError,
  Pane,
  PaneHeader,
  useCan,
  useCollapseNav,
  useLanguage,
  useNarrow,
  useT,
  useTimeZone,
} from "@go-tenon/ui"
import { Fragment, type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { languageName } from "../languages"
import { NARROW_QUERY } from "../layout"
import { type LightboxImage, MessageAttachments } from "../inbox/Attachments"
import { useConversationChannel } from "../inbox/channel"
import { useMe, useMembers, useTeams, useTimeline } from "../inbox/data"
import { describeEvent } from "../inbox/events"
import { type FillWatcher, useFillVisible } from "../inbox/fill"
import { clock, dayLabel, fullDateTime, sameDay } from "../inbox/format"
import { translationOf } from "../inbox/Timeline"
import { TICKET_STATUSES, ticketStatusColor, ticketStatusLabel, ticketVisibilityLabel, useTicket } from "./meta"
import { TicketComposer } from "./TicketComposer"
import { TicketDetails } from "./TicketDetails"

const DETAILS_STORAGE_KEY = "agents-desk:ticket-details-open"

function readSavedDetails(): boolean {
  try {
    const raw = localStorage.getItem(DETAILS_STORAGE_KEY)
    return raw === null ? true : raw === "1"
  } catch {
    return true
  }
}

function saveDetails(open: boolean): void {
  try {
    localStorage.setItem(DETAILS_STORAGE_KEY, open ? "1" : "0")
  } catch {
    // 浏览器禁用存储时仅在当前会话生效
  }
}

/**
 * TicketView 是单张工单的协作主工作区：
 * 顶栏展示 #编号、可就地编辑标题、类型与可见范围、译文/原文切换、4 状态下拉、指派菜单和详情栏开关；
 * 中间时间线展示系统事件、跨语言自动互译的内部讨论备注和对外同步的公开进度消息；
 * 底部是双模式输入卡片（内部讨论 / 公开进度），右侧是工单字段与关联会话栏。
 */
export function TicketView({ id, tenantId, back }: { id: string; tenantId: string; back: () => void }): ReactNode {
  const t = useT()
  const can = useCan()
  const narrow = useNarrow(NARROW_QUERY)
  const wide = useMediaQuery("(min-width: 1600px)") ?? false
  const canInlineDetails = useMediaQuery("(min-width: 1360px)") ?? false
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members, byId } = useMembers()
  const teams = useTeams()
  const ticketQuery = useTicket(id)
  const timeline = useTimeline(id)
  const { viewers, typing, notifyTyping, clearTyping } = useConversationChannel(tenantId, id, me.data?.id)

  const [details, setDetails] = useState(() => (narrow ? false : readSavedDetails()))
  const [original, setOriginal] = useState(false)
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleDraft, setTitleDraft] = useState("")
  const { ref: mainRef, width: mainWidth } = useElementSize()
  useCollapseNav(details && canInlineDetails && !wide && !narrow)

  useEffect(() => {
    if (narrow) setDetails(false)
  }, [narrow])

  const closeDetails = useCallback(() => {
    setDetails(false)
    if (!narrow) saveDetails(false)
  }, [narrow])
  const toggleDetails = useCallback(() => {
    setDetails((prev) => {
      const next = !prev
      if (!narrow) saveDetails(next)
      return next
    })
  }, [narrow])

  const tk = ticketQuery.data
  const last = timeline.data?.at(-1)
  useEffect(() => {
    if (last?.kind === "message" || last?.kind === "note") {
      clearTyping(last.actor.id)
    }
  }, [last, clearTyping])

  // 页面在前台且出现更新的时间线项时标记已读
  const lastSeq = last?.seq ?? 0
  const marked = useRef(0)
  useEffect(() => {
    if (!tk || lastSeq <= Math.max(tk.read_seq, marked.current) || document.visibilityState !== "visible") return
    marked.current = lastSeq
    void tenantApi.conversation.read(id, { seq: lastSeq }).then(() => {
      void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.inbox().queryKey })
    })
  }, [tk, id, lastSeq, queryClient])

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
    void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
  }
  const setStatus = useMutation({
    mutationFn: (status: TicketStatus) => tenantApi.ticket.status(id, { status }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const saveTitle = useMutation({
    mutationFn: (title: string) => tenantApi.ticket.update(id, { title }),
    onSuccess: () => {
      setEditingTitle(false)
      invalidate()
    },
    onError: (err) => notifyError(err),
  })
  const assign = useMutation({
    mutationFn: (body: { assignee_id?: string; team_id?: string }) => tenantApi.conversation.assign(id, body),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })

  if (!tk) {
    return (
      <Center style={{ flex: 1 }}>
        {ticketQuery.isError ? <Text c="dimmed">{t("tickets.not_found")}</Text> : <Loader size="sm" />}
      </Center>
    )
  }

  if (narrow && details) return <TicketDetails ticket={tk} onClose={closeDetails} />

  const myId = me.data?.id
  const myLang = me.data?.language
  const hasTrans = Boolean(
    timeline.data?.some(
      (it) => (it.kind === "note" || it.kind === "message") && it.actor.id !== myId && Boolean(translationOf(it, myLang)),
    ),
  )
  const assignee = tk.assignee?.type === "member" ? (tk.assignee.id === myId ? t("inbox.you") : byId.get(tk.assignee.id)?.name) : undefined
  const teamRow = tk.team_id ? teams.find((x) => x.id === tk.team_id) : undefined
  const meta = [
    tk.type_name,
    teamRow?.name,
    assignee ? t("inbox.assigned_to", { who: assignee }) : t("inbox.no_assignee"),
    tk.linked_count > 0 ? t("tickets.linked_count", { n: tk.linked_count }) : undefined,
  ]
    .filter(Boolean)
    .join(" · ")

  const inlineDetails = details && !narrow && canInlineDetails
  const drawerDetails = details && !narrow && !canInlineDetails
  const compactActions = narrow || (mainWidth > 0 && mainWidth < 680)
  const canEdit = can("conversations:reply")
  const canAssignOthers = can("conversations:assign")
  const activeTeams = teams.filter((x) => x.status === "active")
  const assignedToMe = tk.assignee?.type === "member" && tk.assignee.id === myId

  const commitTitle = () => {
    const trimmed = titleDraft.trim()
    if (!trimmed || trimmed === tk.title) {
      setEditingTitle(false)
      return
    }
    saveTitle.mutate(trimmed)
  }

  return (
    <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
      <Box ref={mainRef} style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
        <PaneHeader fixed={!narrow} back={narrow ? back : undefined} style={narrow ? { paddingBlock: 8 } : undefined}>
          <Box style={{ minWidth: 0, flex: 1 }}>
            <Group gap={6} wrap="nowrap">
              <Text size="sm" fw={700} c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                #{tk.number}
              </Text>
              {editingTitle ? (
                <TextInput
                  size="xs"
                  value={titleDraft}
                  onChange={(e) => setTitleDraft(e.currentTarget.value)}
                  onBlur={commitTitle}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && !e.nativeEvent.isComposing) commitTitle()
                    if (e.key === "Escape") setEditingTitle(false)
                  }}
                  maxLength={120}
                  autoFocus
                  style={{ flex: 1, minWidth: 0 }}
                />
              ) : (
                <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
                  <Text
                    size="md"
                    fw={600}
                    truncate
                    lh={1.25}
                    onDoubleClick={() => {
                      if (!canEdit) return
                      setTitleDraft(tk.title)
                      setEditingTitle(true)
                    }}
                  >
                    {tk.title}
                  </Text>
                  {canEdit && (
                    <Tooltip label={t("tickets.edit_title")} openDelay={300}>
                      <ActionIcon
                        size={20}
                        variant="subtle"
                        color="gray"
                        aria-label={t("tickets.edit_title")}
                        onClick={() => {
                          setTitleDraft(tk.title)
                          setEditingTitle(true)
                        }}
                      >
                        <IconPencil size={12} />
                      </ActionIcon>
                    </Tooltip>
                  )}
                </Group>
              )}
              <Badge
                size="xs"
                variant="light"
                color={tk.visibility === "all" ? "bright-blue" : "gray"}
                leftSection={tk.visibility === "all" ? <IconWorld size={10} /> : <IconLock size={10} />}
                style={{ flex: "none" }}
              >
                {ticketVisibilityLabel(tk.visibility, t)}
              </Badge>
            </Group>
            <Text size="xs" c="dimmed" truncate lh={1.3}>
              {meta}
            </Text>
          </Box>

          {viewers.length > 0 && !compactActions && (
            <Tooltip label={t("inbox.viewing", { who: viewers.map((v) => v.name).join(", ") })}>
              <Avatar.Group spacing="xs" style={{ flex: "none" }}>
                {viewers.slice(0, 3).map((v) => (
                  <Avatar key={v.id} size={26} name={v.name} color="initials" src={byId.get(v.id)?.avatar_url || undefined} />
                ))}
              </Avatar.Group>
            </Tooltip>
          )}

          <Group
            gap={4}
            wrap="nowrap"
            w={narrow ? "100%" : undefined}
            justify={narrow ? "flex-end" : undefined}
            style={{ flex: "none" }}
            data-tn-density="dense"
          >
            {hasTrans && (
              <>
                <SegmentedControl
                  value={original ? "original" : "translation"}
                  onChange={(v) => setOriginal(v === "original")}
                  data={[
                    { value: "translation", label: t("inbox.translated") },
                    { value: "original", label: t("inbox.original") },
                  ]}
                  aria-label={t("inbox.text_shown")}
                />
                <Divider orientation="vertical" h={16} mx={2} style={{ alignSelf: "center" }} />
              </>
            )}

            <Menu position="bottom-end" withinPortal>
              <Menu.Target>
                <Button
                  variant="light"
                  color={ticketStatusColor[tk.status]}
                  rightSection={<IconChevronDown size={12} />}
                  loading={setStatus.isPending}
                  disabled={!canEdit}
                >
                  {ticketStatusLabel(tk.status, t)}
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                {TICKET_STATUSES.map((st) => (
                  <Menu.Item
                    key={st}
                    disabled={tk.status === st}
                    rightSection={tk.status === st ? <IconCheck size={13} /> : undefined}
                    onClick={() => setStatus.mutate(st)}
                  >
                    <Group gap={6} wrap="nowrap">
                      <Box w={8} h={8} bg={ticketStatusColor[st]} style={{ borderRadius: "50%" }} />
                      <Text size="sm">{ticketStatusLabel(st, t)}</Text>
                    </Group>
                  </Menu.Item>
                ))}
              </Menu.Dropdown>
            </Menu>

            {canEdit && (
              <Menu position="bottom-end" withinPortal>
                <Menu.Target>
                  {compactActions ? (
                    <Tooltip label={t("inbox.assign")}>
                      <ActionIcon variant="subtle" color="gray" size="input-sm" loading={assign.isPending} aria-label={t("inbox.assign")}>
                        <IconUserPlus size={15} />
                      </ActionIcon>
                    </Tooltip>
                  ) : (
                    <Button
                      variant="subtle"
                      color="gray"
                      leftSection={<IconUserPlus size={14} />}
                      rightSection={<IconChevronDown size={12} />}
                      loading={assign.isPending}
                    >
                      {t("inbox.assign")}
                    </Button>
                  )}
                </Menu.Target>
                <Menu.Dropdown mah={420} style={{ overflowY: "auto" }}>
                  {me.data && !assignedToMe && (
                    <Menu.Item onClick={() => assign.mutate({ assignee_id: me.data.id })}>{t("inbox.assign_me")}</Menu.Item>
                  )}
                  {canAssignOthers && (
                    <>
                      <Menu.Label>{t("inbox.assign_members")}</Menu.Label>
                      {members
                        .filter((m) => m.id !== myId)
                        .map((m) => (
                          <Menu.Item
                            key={m.id}
                            disabled={tk.assignee?.id === m.id}
                            rightSection={m.status === "online" ? undefined : t("inbox.away")}
                            onClick={() => assign.mutate({ assignee_id: m.id })}
                          >
                            {m.name}
                          </Menu.Item>
                        ))}
                      <Menu.Label>{t("inbox.assign_teams")}</Menu.Label>
                      {activeTeams.map((team) => (
                        <Menu.Item key={team.id} disabled={tk.team_id === team.id} onClick={() => assign.mutate({ team_id: team.id })}>
                          {team.name}
                        </Menu.Item>
                      ))}
                      {tk.assignee && (
                        <>
                          <Menu.Divider />
                          <Menu.Item onClick={() => assign.mutate({ assignee_id: "" })}>{t("inbox.unassign")}</Menu.Item>
                        </>
                      )}
                    </>
                  )}
                </Menu.Dropdown>
              </Menu>
            )}

            <Divider orientation="vertical" h={16} mx={2} style={{ alignSelf: "center" }} />
            <Tooltip label={t("inbox.details")}>
              <ActionIcon
                variant={details ? "light" : "subtle"}
                color={details ? undefined : "gray"}
                size="input-sm"
                onClick={toggleDetails}
                aria-label={t("inbox.details")}
              >
                <IconLayoutSidebarRight size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </PaneHeader>

        <TicketTimeline ticket={tk} original={original} typingNames={typing.map((x) => x.name ?? "").filter(Boolean)} />

        <TicketComposer key={tk.id} ticket={tk} onTyping={notifyTyping} />
      </Box>

      {inlineDetails && (
        <Pane border="left" resizable={{ storageKey: "ticket-details", defaultWidth: 340, min: 280, max: 440, side: "left" }}>
          <TicketDetails ticket={tk} onClose={closeDetails} />
        </Pane>
      )}
      {!narrow && !canInlineDetails && (
        <Drawer opened={drawerDetails} onClose={closeDetails} position="right" size={350} padding={0} withCloseButton={false}>
          <Box h="100dvh">
            <TicketDetails ticket={tk} onClose={closeDetails} />
          </Box>
        </Drawer>
      )}
    </Group>
  )
}

/** TicketTimeline 渲染工单的协作时间线：系统事件、跨语言内部讨论备注和同步至客户的公开进度消息。 */
function TicketTimeline({
  ticket,
  original,
  typingNames,
}: {
  ticket: ConversationTicketDTO
  original: boolean
  typingNames: string[]
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const items = useTimeline(ticket.id)
  const translation = useQuery(kit.queries.translation.settings())
  const translating = translation.data?.enabled ?? false
  const watch = useFillVisible(translating, me.data?.language)
  const scroller = useRef<HTMLDivElement>(null)
  const [lightbox, setLightbox] = useState<LightboxImage | null>(null)
  const list = items.data ?? []

  useEffect(() => {
    if (items.isPending) return
    const el = scroller.current
    if (el) el.scrollTop = el.scrollHeight
  }, [items.isPending, list.length])

  const names = {
    member: (id: string) => (id === me.data?.id ? t("inbox.you") : (byId.get(id)?.name ?? t("event.someone"))),
    team: (id: string) => teams.find((x) => x.id === id)?.name ?? "",
  }

  if (items.isPending) {
    return (
      <Box className="inbox-timeline" px="var(--tn-pane-px)" py="md">
        <Stack gap="md">
          <Skeleton h={56} radius="md" />
          <Skeleton h={64} radius="md" />
          <Skeleton h={48} radius="md" />
        </Stack>
      </Box>
    )
  }

  return (
    <Box className="inbox-timeline-wrap">
      <Box ref={scroller} className="inbox-timeline">
        <Stack gap={0} px="var(--tn-pane-px)" py="md">
          {list.map((it, i) => {
            const prev = list[i - 1]
            const showDay = i === 0 || !sameDay(prev.created_at, it.created_at)
            const gapTop = i === 0 ? 0 : 12
            return (
              <Fragment key={it.id}>
                {showDay && <Divider my={i === 0 ? 4 : 12} label={dayLabel(it.created_at, ui, t, zone)} labelPosition="center" />}
                {it.kind === "event" ? (
                  <Text size="xs" c="dimmed" ta="center" mt={gapTop} className="inbox-event">
                    {describeEvent(it, byId, names, t, ui)} · {clock(it.created_at, ui, zone)}
                  </Text>
                ) : (
                  <TicketTimelineCard
                    ticket={ticket}
                    it={it}
                    author={names.member(it.actor.id)}
                    isOwn={it.actor.type === "member" && it.actor.id === me.data?.id}
                    time={clock(it.created_at, ui, zone)}
                    fullTime={fullDateTime(it.created_at, ui, zone)}
                    lang={me.data?.language}
                    original={original}
                    translating={translating}
                    watch={watch}
                    mt={gapTop}
                    onPreview={setLightbox}
                  />
                )}
              </Fragment>
            )
          })}
          {typingNames.length > 0 && (
            <Box mt={10} className="inbox-typing">
              <span className="inbox-typing-dots" aria-hidden="true">
                <span />
                <span />
                <span />
              </span>
              <Text size="xs" c="dimmed">
                {t("inbox.typing", { who: typingNames.join(", ") })}
              </Text>
            </Box>
          )}
        </Stack>
      </Box>
      <ImageLightbox src={lightbox?.src ?? null} filename={lightbox?.filename} onClose={() => setLightbox(null)} />
    </Box>
  )
}

/** TicketTimelineCard 渲染工单中的单条内部讨论备注或公开进度消息，支持跨语言译文展示与原文切换。 */
function TicketTimelineCard({
  ticket,
  it,
  author,
  isOwn,
  time,
  fullTime,
  lang,
  original,
  translating,
  watch,
  mt,
  onPreview,
}: {
  ticket: ConversationTicketDTO
  it: ConversationItemDTO
  author: string
  isOwn: boolean
  time: string
  fullTime: string
  lang: string | undefined
  original: boolean
  translating: boolean
  watch: FillWatcher
  mt: number
  onPreview: (img: LightboxImage) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [prevOriginal, setPrevOriginal] = useState(original)
  const [flip, setFlip] = useState(false)
  if (prevOriginal !== original) {
    setPrevOriginal(original)
    setFlip(false)
  }

  const isPublic = it.kind === "message" && it.visibility === "all"
  const raw = it.content.text ?? ""
  const translated = isOwn ? undefined : translationOf(it, lang)
  const showOriginal = original !== flip
  const shown = translated && !showOriginal ? translated : raw
  const fromLang = it.derived.language as string | undefined
  const needFill = !isOwn && translating && it.untranslated

  return (
    <Paper
      ref={needFill ? watch(it.id) : undefined}
      mt={mt}
      px="sm"
      py={10}
      radius="md"
      withBorder={isPublic}
      bg={isPublic ? "var(--mantine-primary-color-light)" : "var(--mantine-color-yellow-light)"}
      className={isPublic ? undefined : "inbox-note"}
      style={
        isPublic
          ? { borderLeft: "3px solid var(--mantine-primary-color-filled)" }
          : undefined
      }
    >
      <Group gap={6} mb={4} wrap="nowrap">
        {isPublic ? (
          <IconWorld size={13} color="var(--mantine-primary-color-filled)" style={{ flex: "none" }} />
        ) : (
          <IconLock size={12} className="inbox-note-accent" style={{ flex: "none" }} />
        )}
        <Text size="xs" fw={600} c={isPublic ? "brand" : undefined} className={isPublic ? undefined : "inbox-note-accent"}>
          {isPublic ? `${t("tickets.public_badge")} · ${author}` : t("inbox.note_by", { who: author })}
        </Text>
        <Text size="xs" c="dimmed">
          ·
        </Text>
        <Tooltip label={fullTime} openDelay={300}>
          <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
            {time}
          </Text>
        </Tooltip>
        {shown && (
          <Box ml="auto">
            <CopyButton value={shown} timeout={1500}>
              {({ copied, copy }) => (
                <Tooltip label={t(copied ? "inbox.copied" : "inbox.copy_message")} openDelay={250}>
                  <ActionIcon variant="subtle" color={copied ? "green" : "gray"} size={22} onClick={copy} aria-label={t("inbox.copy_message")}>
                    {copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
                  </ActionIcon>
                </Tooltip>
              )}
            </CopyButton>
          </Box>
        )}
      </Group>

      {shown && (
        <Text size="md" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
          {shown.split(/(@\S+)/).map((part, i) =>
            !isPublic && part.startsWith("@") ? (
              // biome-ignore lint/suspicious/noArrayIndexKey: 拆分出的文本片段顺序固定
              <Text key={i} span fw={700} className="inbox-note-accent">
                {part}
              </Text>
            ) : (
              // biome-ignore lint/suspicious/noArrayIndexKey: 拆分出的文本片段顺序固定
              <Fragment key={i}>{part}</Fragment>
            ),
          )}
        </Text>
      )}

      <MessageAttachments conversationId={ticket.id} attachments={it.attachments} hasText={Boolean(shown)} onPreview={onPreview} />

      {(translated || needFill || (isPublic && ticket.linked_count > 0)) && (
        <Group gap={6} mt={6} wrap="wrap">
          {translated ? (
            <>
              <IconLanguage size={12} className="inbox-caption-icon" />
              {fromLang && (
                <>
                  <Text size="xs" c="dimmed">
                    {languageName(fromLang, ui)}
                  </Text>
                  <Text size="xs" c="dimmed">
                    ·
                  </Text>
                </>
              )}
              <UnstyledButton className="inbox-caption-btn" onClick={() => setFlip((f) => !f)}>
                {t(showOriginal ? "inbox.view_translation" : "inbox.view_original")}
              </UnstyledButton>
            </>
          ) : needFill ? (
            <>
              <Loader size={10} />
              <Text size="xs" c="dimmed">
                {t("inbox.translating")}
              </Text>
            </>
          ) : null}
          {isPublic && ticket.linked_count > 0 && (
            <Text size="xs" c="dimmed" ml={translated || needFill ? "auto" : undefined}>
              {t("tickets.public_synced_hint", { n: ticket.linked_count })}
            </Text>
          )}
        </Group>
      )}
    </Paper>
  )
}
