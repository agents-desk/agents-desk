import type { ConversationItemDTO, ConversationTicketDTO, TicketStatus } from "@agents-desk/client/tenant"
import { ActionIcon, Avatar, Badge, Box, Button, Divider, Group, Menu, Stack, Text, TextInput, Tooltip } from "@mantine/core"
import { useElementSize } from "@mantine/hooks"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconCheck, IconChevronDown, IconLanguage, IconLayoutSidebarRight, IconLock, IconPencil, IconWorld } from "@tabler/icons-react"
import {
  AsidePane,
  EmptyState,
  ImageLightbox,
  InspectorSkeleton,
  notifyError,
  PaneHeader,
  StatusDot,
  useAsidePaneState,
  useCan,
  useLanguage,
  useNarrow,
  useT,
  useTimeZone,
} from "@go-tenon/ui"
import { Fragment, type ReactNode, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { isTypingTarget, NARROW_QUERY } from "../layout"
import type { LightboxImage } from "../inbox/Attachments"
import { useConversationChannel } from "../inbox/channel"
import { verifiedTranslationOf } from "../inbox/ComposerTranslation"
import { useMe, useMembers, useTeams, useTimeline } from "../inbox/data"
import { describeEvent } from "../inbox/events"
import { type FillWatcher, useFillVisible } from "../inbox/fill"
import { clock, dayLabel, fullDateTime, sameDay } from "../inbox/format"
import { AssignMenu } from "../inbox/menus"
import {
  ActorAvatar,
  canGroupItems,
  MessageBubble,
  TranslationCaption,
  translationOf,
  TypingIndicator,
  useSyncRead,
} from "../inbox/MessagePrimitives"
import { invalidateTicket, TICKET_STATUSES, ticketStatusColor, ticketStatusLabel, ticketVisibilityLabel, useTicket } from "./meta"
import { TicketComposer } from "./TicketComposer"
import { TicketDetails } from "./TicketDetails"
import { firstUnread, NewMessagesDivider, ScrollToBottomButton, useStickToBottom } from "../inbox/stick-to-bottom"
import { ConversationSkeleton, TimelineSkeleton } from "../components/detail-skeleton"

/** isPublicUpdate 判断工单时间线项是否为对外同步的公开进度消息。 */
function isPublicUpdate(it: ConversationItemDTO): boolean {
  return it.kind === "message" && it.visibility === "all"
}

/**
 * TicketView 是单张工单的协作主工作区：
 * 顶栏展示 #编号、可就地编辑标题、类型与可见范围、4 状态下拉、指派菜单和详情栏开关；
 * 中间时间线以聊天气泡流展示系统事件、跨语言自动互译的内部讨论和对外同步的公开进度消息；
 * 底部是双模式输入卡片（内部讨论 / 公开进度），右侧由框架 AsidePane 承载工单字段与受影响客户详情栏。
 */
export function TicketView({ id, tenantId, back }: { id: string; tenantId: string; back: () => void }): ReactNode {
  const t = useT()
  const can = useCan()
  const narrow = useNarrow(NARROW_QUERY)
  const queryClient = useQueryClient()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const ticketQuery = useTicket(id)
  const timeline = useTimeline(id)
  const { viewers, typing, notifyTyping, clearTyping } = useConversationChannel(tenantId, id, me.data?.id)

  const details = useAsidePaneState("ticket-details", { defaultOpen: true, compactQuery: NARROW_QUERY })
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleDraft, setTitleDraft] = useState("")
  const [showOriginalTitle, setShowOriginalTitle] = useState(false)
  const { ref: mainRef, width: mainWidth } = useElementSize()
  const translation = useQuery(kit.queries.translation.settings())
  const translating = translation.data?.enabled ?? false

  const tk = ticketQuery.data
  // 工单标题或描述缺少当前成员语言译文时，自动触发后台补译（译好后由实时推送刷新）
  const filledMetaRef = useRef("")
  useEffect(() => {
    if (!tk || !translating || !tk.meta_untranslated || !me.data?.language) return
    const key = `${tk.id}:${me.data.language}:${tk.updated_at}`
    if (filledMetaRef.current === key) return
    filledMetaRef.current = key
    void tenantApi.translation.fill({ ticket_ids: [tk.id] })
  }, [tk, translating, me.data?.language])
  useEffect(() => {
    setShowOriginalTitle(false)
  }, [tk?.id])
  // 打开时读到的位置：“新消息”分隔线画在它之后，拿到真实详情数据后才锁定，列表占位数据只用于显示
  const readAtOpen = useRef<number | null>(null)
  if (tk && !ticketQuery.isPlaceholderData && readAtOpen.current === null) readAtOpen.current = tk.read_seq
  const last = timeline.data?.at(-1)
  useEffect(() => {
    if (last?.kind === "message" || last?.kind === "note") {
      clearTyping(last.actor.id)
    }
  }, [last, clearTyping])

  // 页面在前台且出现更新的时间线项时标记已读；标签页从后台切回前台时同步补标已读
  useSyncRead({
    ready: Boolean(tk),
    readSeq: tk?.read_seq ?? 0,
    lastSeq: last?.seq ?? 0,
    onMark: (seq) => {
      void tenantApi.conversation.read(id, { seq }).then(() => {
        void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.get(id).queryKey })
        void queryClient.invalidateQueries({ queryKey: [...kit.queries.ticket.key(), "list"] })
        void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.inbox().queryKey })
      })
    },
  })

  const invalidate = () => invalidateTicket(queryClient, id)
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

  // 工单的快捷键（非输入态生效）：I 切换详情栏；R、N 由输入框处理
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return
      if (e.key.toLowerCase() === "i") {
        e.preventDefault()
        details.toggle()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [details.toggle])

  // 还没有数据（直接打开链接或网络较慢）时显示与页面同样布局的骨架，详情栏保持原来的开合，数据到达后只替换内容
  if (!tk) {
    if (ticketQuery.isError) {
      return (
        <Box style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
          {narrow && <PaneHeader back={back} />}
          <EmptyState fill title={t("tickets.not_found")} />
        </Box>
      )
    }
    return (
      <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
        <ConversationSkeleton back={narrow ? back : undefined} />
        <AsidePane
          opened={details.opened}
          onClose={details.close}
          compactQuery={NARROW_QUERY}
          resizable={{ storageKey: "ticket-details", defaultWidth: 320, min: 260, max: 420 }}
        >
          <InspectorSkeleton onClose={details.close} closeLabel={t("inbox.close_details")} />
        </AsidePane>
      </Group>
    )
  }

  const myId = me.data?.id
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

  const compactActions = narrow || (mainWidth > 0 && mainWidth < 680)
  const canEdit = can("conversations:reply")

  const commitTitle = () => {
    if (!editingTitle || saveTitle.isPending) return
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
                    {tk.title_translated && !showOriginalTitle ? tk.title_translated : tk.title}
                  </Text>
                  {tk.title_translated && (
                    <Tooltip label={t(showOriginalTitle ? "inbox.view_translation" : "inbox.view_original")} openDelay={300}>
                      <ActionIcon
                        size={20}
                        variant={showOriginalTitle ? "light" : "subtle"}
                        color={showOriginalTitle ? undefined : "gray"}
                        aria-label={t(showOriginalTitle ? "inbox.view_translation" : "inbox.view_original")}
                        onClick={() => setShowOriginalTitle((v) => !v)}
                      >
                        <IconLanguage size={13} />
                      </ActionIcon>
                    </Tooltip>
                  )}
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
                    rightSection={tk.status === st ? <IconCheck size={14} /> : undefined}
                    onClick={() => setStatus.mutate(st)}
                  >
                    <Group gap={6} wrap="nowrap">
                      <StatusDot color={ticketStatusColor[st]} />
                      <Text size="sm">{ticketStatusLabel(st, t)}</Text>
                    </Group>
                  </Menu.Item>
                ))}
              </Menu.Dropdown>
            </Menu>

            {canEdit && <AssignMenu c={tk} compact={compactActions} onSuccess={invalidate} />}

            <Divider orientation="vertical" h={16} mx={2} style={{ alignSelf: "center" }} />
            <Tooltip label={t("inbox.details")}>
              <ActionIcon
                variant={details.opened ? "light" : "subtle"}
                color={details.opened ? undefined : "gray"}
                size="input-sm"
                onClick={details.toggle}
                aria-label={t("inbox.details")}
              >
                <IconLayoutSidebarRight size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </PaneHeader>

        <TicketTimeline ticket={tk} readSeq={readAtOpen.current ?? 0} typingNames={typing.map((x) => x.name ?? "").filter(Boolean)} />

        <TicketComposer key={tk.id} ticket={tk} onTyping={notifyTyping} />
      </Box>

      <AsidePane
        opened={details.opened}
        onClose={details.close}
        compactQuery={NARROW_QUERY}
        resizable={{ storageKey: "ticket-details", defaultWidth: 320, min: 260, max: 420 }}
      >
        <TicketDetails ticket={tk} onClose={details.close} />
      </AsidePane>
    </Group>
  )
}

/** TicketTimeline 渲染工单的聊天式协作时间线：系统事件、连续分组的内部讨论气泡和对外同步的公开进度气泡。 */
function TicketTimeline({
  ticket,
  readSeq,
  typingNames,
}: {
  ticket: ConversationTicketDTO
  readSeq: number
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
  const [lightbox, setLightbox] = useState<LightboxImage | null>(null)
  const list = items.data ?? []
  const stick = useStickToBottom({ ready: !items.isPending, list, myId: me.data?.id, typingCount: typingNames.length })

  const names = {
    member: (id: string) => (id === me.data?.id ? t("inbox.you") : (byId.get(id)?.name ?? t("event.someone"))),
    team: (id: string) => teams.find((x) => x.id === id)?.name ?? "",
  }

  if (items.isPending) {
    return (
      <Box className="inbox-timeline" px="var(--tn-pane-px)" py="md">
        <TimelineSkeleton />
      </Box>
    )
  }

  const firstNew = firstUnread(list, readSeq, me.data?.id)
  const sameKind = (a: ConversationItemDTO, b: ConversationItemDTO) => isPublicUpdate(a) === isPublicUpdate(b)
  const memberOf = (memberId: string) => byId.get(memberId) ?? (memberId === me.data?.id ? me.data : undefined)

  return (
    <Box className="inbox-timeline-wrap">
      <Box ref={stick.scroller} className="inbox-timeline" onScroll={stick.onScroll}>
        <Stack gap={0} px="var(--tn-pane-px)" py="md">
          {list.map((it, i) => {
            const prev = list[i - 1]
            const next = list[i + 1]
            const showDay = i === 0 || !sameDay(prev.created_at, it.created_at, zone)
            const showNew = it === firstNew
            const groupedWithPrev = !showDay && !showNew && canGroupItems(prev, it, firstNew, sameKind, zone)
            const groupedWithNext = Boolean(next) && canGroupItems(it, next, firstNew, sameKind, zone)
            const gapTop = i === 0 ? 0 : groupedWithPrev ? 4 : 16
            const author = names.member(it.actor.id)
            return (
              <Fragment key={it.id}>
                {showDay && <Divider my={i === 0 ? 4 : 12} label={dayLabel(it.created_at, ui, t, zone)} labelPosition="center" />}
                {showNew && <NewMessagesDivider />}
                {it.kind === "event" ? (
                  <Text size="xs" c="dimmed" ta="center" mt={gapTop} className="inbox-event">
                    {describeEvent(it, byId, names, t, ui, zone)} · {clock(it.created_at, ui, zone)}
                  </Text>
                ) : (
                  <TicketBubble
                    ticket={ticket}
                    it={it}
                    author={author}
                    avatar={<ActorAvatar actor={it.actor} size={32} member={memberOf(it.actor.id)} fallbackName={author} />}
                    isOwn={it.actor.type === "member" && it.actor.id === me.data?.id}
                    time={clock(it.created_at, ui, zone)}
                    fullTime={fullDateTime(it.created_at, ui, zone)}
                    first={!groupedWithPrev}
                    last={!groupedWithNext}
                    lang={me.data?.language}
                    translating={translating}
                    watch={watch}
                    mt={gapTop}
                    onPreview={setLightbox}
                  />
                )}
              </Fragment>
            )
          })}
          <TypingIndicator names={typingNames} />
        </Stack>
      </Box>
      <ScrollToBottomButton stick={stick} />
      <ImageLightbox src={lightbox?.src ?? null} filename={lightbox?.filename} onClose={() => setLightbox(null)} />
    </Box>
  )
}

/** TicketBubble 渲染工单中的单条聊天气泡：当前成员居右、其他同事居左，两侧均带 32px 头像，公开进度附带品牌标记与客户同步计数。 */
function TicketBubble({
  ticket,
  it,
  author,
  avatar,
  isOwn,
  time,
  fullTime,
  first,
  last,
  lang,
  translating,
  watch,
  mt,
  onPreview,
}: {
  ticket: ConversationTicketDTO
  it: ConversationItemDTO
  author: string
  avatar: ReactNode
  isOwn: boolean
  time: string
  fullTime: string
  first: boolean
  last: boolean
  lang: string | undefined
  translating: boolean
  watch: FillWatcher
  mt: number
  onPreview: (img: LightboxImage) => void
}): ReactNode {
  const t = useT()
  const [showOriginal, setShowOriginal] = useState(false)
  const [showVerified, setShowVerified] = useState(false)

  const isPublic = isPublicUpdate(it)
  const broadcastCount = typeof it.derived.broadcast_count === "number" ? it.derived.broadcast_count : undefined
  const showBroadcastHint = isPublic && (broadcastCount !== undefined ? broadcastCount > 0 : ticket.linked_count > 0)
  const raw = it.content.text ?? ""
  const translated = isOwn ? undefined : translationOf(it, lang)
  const verified = isOwn ? verifiedTranslationOf(it) : undefined
  const shown = translated && !showOriginal ? translated : raw
  const fromLang = it.derived.language as string | undefined
  const needFill = !isOwn && translating && it.untranslated

  const hasCaption = Boolean(translated || needFill || verified)
  const caption = hasCaption ? (
    <TranslationCaption
      translated={translated}
      needFill={needFill}
      fromLang={fromLang}
      showOriginal={showOriginal}
      onToggleOriginal={() => setShowOriginal((v) => !v)}
      verifiedLang={verified?.language}
      showVerified={showVerified}
      onToggleVerified={() => setShowVerified((v) => !v)}
    />
  ) : undefined

  const broadcastNode = showBroadcastHint ? (
    <Text size="xs" c="dimmed">
      {broadcastCount !== undefined ? t("tickets.public_synced_hint", { n: broadcastCount }) : t("tickets.public_syncing")}
    </Text>
  ) : undefined

  const publicBadge = isPublic ? (
    <Badge size="xs" variant="light" color="brand" leftSection={<IconWorld size={10} />} style={{ flex: "none" }}>
      {t("tickets.public_badge")}
    </Badge>
  ) : undefined

  return (
    <MessageBubble
      conversationId={ticket.id}
      attachments={it.attachments}
      seq={it.seq}
      watchRef={needFill ? watch(it.id) : undefined}
      mine={isOwn}
      first={first}
      last={last}
      mt={mt}
      author={author}
      avatar={avatar}
      headerExtra={publicBadge}
      time={time}
      fullTime={fullTime}
      text={shown || undefined}
      mentions={!isPublic}
      original={Boolean(translated && showOriginal)}
      publicUpdate={isPublic}
      expandedTranslation={verified && showVerified ? verified : undefined}
      caption={caption}
      footerExtra={broadcastNode}
      onPreview={onPreview}
    />
  )
}
