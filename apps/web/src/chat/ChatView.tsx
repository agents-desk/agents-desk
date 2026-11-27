import type {
  ConversationAutoTranslateRequest,
  ConversationChatDTO,
  ConversationItemDTO,
  ConversationTranslation,
  MemberTenantMember,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Avatar,
  Box,
  Button,
  Center,
  Divider,
  FileButton,
  Group,
  Indicator,
  Loader,
  Menu,
  Modal,
  SegmentedControl,
  Stack,
  Text,
  Textarea,
  ThemeIcon,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconArrowBackUp, IconLayoutSidebarRight, IconLock, IconPaperclip, IconPencil, IconUsersGroup } from "@tabler/icons-react"
import {
  AsidePane,
  ImageLightbox,
  notifyError,
  PaneHeader,
  TriggerMenu,
  textTriggerRule,
  useAsidePaneState,
  useLanguage,
  useNarrow,
  useT,
  useTextTrigger,
  useTimeZone,
  useUploadQueue,
} from "@go-tenon/ui"
import { Fragment, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { ContactAvatar } from "../contacts/display"
import {
  ATTACHMENT_ACCEPT,
  AttachmentStrip,
  attachmentMaxSize,
  type LightboxImage,
  MAX_ATTACHMENTS,
  toAttachmentInput,
  type UploadedMedia,
  uploadMediaFile,
} from "../inbox/Attachments"
import { useConversationChannel } from "../inbox/channel"
import { channelColor, ChannelChip } from "../inbox/channel-badge"
import {
  ComposerSendArea,
  InlineTranslationPane,
  resolveInternalTargetLanguage,
  useComposerTranslation,
  verifiedTranslationOf,
} from "../inbox/ComposerTranslation"
import { useMe, useMembers, useTeams, useTimeline } from "../inbox/data"
import { describeEvent } from "../inbox/events"
import { type FillWatcher, useFillVisible } from "../inbox/fill"
import { clock, dayLabel, fullDateTime, sameDay } from "../inbox/format"
import {
  ActorAvatar,
  canGroupItems,
  deliveryColor,
  MessageBubble,
  TimelineNote,
  TranslationCaption,
  translationOf,
  TypingIndicator,
  useSyncRead,
} from "../inbox/MessagePrimitives"
import { languageName, sameLanguage } from "../languages"
import { isTypingTarget, NARROW_QUERY } from "../layout"
import { BusinessDetails } from "./BusinessDetails"
import { chatDisplayName, useChat } from "./data"
import { GroupDetails } from "./GroupDetails"
import { PeerDetails } from "./PeerDetails"
import { firstUnread, NewMessagesDivider, ScrollToBottomButton, useStickToBottom } from "../inbox/stick-to-bottom"
import { clearDraft, type DraftMode, getDraft, saveDraft } from "../inbox/drafts"
import { ConversationSkeleton, InspectorSkeleton, TimelineSkeleton } from "../components/detail-skeleton"
import { EmptyState } from "../components/empty-state"

/**
 * ChatView 是单条聊天（内部单聊/群聊 或 商务沟通 1:1/外部群聊）的主对话区：
 * 顶栏展示对方资料/渠道徽章/群成员数与右侧详情栏开关；
 * 中间消息流支持连续消息分组、跨语言自动互译（按阅读者语言默认展示译文并支持单条切换原文）、商务沟通内部备注、外部渠道送达状态与重发、附件灯箱、@高亮和正在输入提示；
 * 底部输入框支持商务沟通“回复 / 内部备注”切换、@ 提及、拖拽/粘贴附件、统一译文校对展开栏及快捷键发送。
 */
export function ChatView({
  id,
  tenantId,
  back,
  onOpenChat,
}: {
  id: string
  tenantId: string
  back: () => void
  onOpenChat: (chatId: string) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const narrow = useNarrow(NARROW_QUERY)
  const queryClient = useQueryClient()
  const me = useMe()
  const chatQuery = useChat(id)
  const timeline = useTimeline(id)
  const { typing, notifyTyping, clearTyping } = useConversationChannel(tenantId, id, me.data?.id)

  const details = useAsidePaneState("chat-details", { compactQuery: NARROW_QUERY })

  const chat = chatQuery.data
  // 打开时读到的位置：“新消息”分隔线画在它之后，读过之后也保持在原处
  const readAtOpen = useRef<number | null>(null)
  if (chat && readAtOpen.current === null) readAtOpen.current = chat.read_seq
  const last = timeline.data?.at(-1)
  useEffect(() => {
    if (last?.kind === "message" || last?.kind === "note") {
      clearTyping(last.actor.type === "contact" ? "contact" : last.actor.id)
    }
  }, [last, clearTyping])

  // 页面在前台且出现更新的消息时推进已读位置，从后台切回前台时同步补标已读
  useSyncRead({
    ready: Boolean(chat),
    readSeq: chat?.read_seq ?? 0,
    lastSeq: last?.seq ?? 0,
    onMark: (seq) => {
      void tenantApi.conversation.read(id, { seq }).then(() => {
        void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
      })
    },
  })

  // 聊天的快捷键（非输入态生效）：I 切换详情栏；R、N 由输入框处理
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
  if (!chat) {
    if (chatQuery.isError) return <EmptyState fill title={t("chat.not_found")} />
    return (
      <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
        <ConversationSkeleton back={narrow ? back : undefined} />
        <AsidePane
          opened={details.opened}
          onClose={details.close}
          compactQuery={NARROW_QUERY}
          resizable={{ storageKey: "chat-details", defaultWidth: 320, min: 260, max: 420 }}
        >
          <InspectorSkeleton onClose={details.close} />
        </AsidePane>
      </Group>
    )
  }

  const isExternal = chat.scope === "external"
  const title = chatDisplayName(chat, t)
  const chColor = chat.channel ? channelColor(chat.channel.kind) : "brand"

  let subtitle: string
  if (isExternal) {
    const langLabel = chat.language ? languageName(chat.language, ui) : ""
    if (chat.mode === "group") {
      subtitle = [t("chat.members_count", { n: chat.participant_count }), langLabel].filter(Boolean).join(" · ")
    } else {
      subtitle = [chat.contact?.email, langLabel].filter(Boolean).join(" · ") || chat.channel?.name || ""
    }
  } else {
    subtitle =
      chat.mode === "group"
        ? t("chat.members_count", { n: chat.participant_count })
        : chat.peer?.status === "online"
          ? t("chat.status_online")
          : t("chat.status_away")
  }

  const detailsLabel = t(isExternal ? "chat.business_details" : chat.mode === "group" ? "chat.group_settings" : "chat.peer_details")

  return (
    <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
      <Box style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", height: "100%" }}>
        <PaneHeader fixed={!narrow} back={narrow ? back : undefined}>
          <Group gap="sm" wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
            {chat.mode === "group" ? (
              <ThemeIcon size={32} radius="xl" variant="light" color={isExternal ? chColor : "brand"} style={{ flex: "none" }}>
                <IconUsersGroup size={16} stroke={1.6} />
              </ThemeIcon>
            ) : isExternal ? (
              <ContactAvatar contact={chat.contact} size={32} />
            ) : (
              <Indicator
                size={8}
                offset={2}
                position="bottom-end"
                color={chat.peer?.status === "online" ? "green" : "gray"}
                withBorder
                style={{ flex: "none" }}
              >
                <Avatar size={32} radius="xl" name={title} color="initials" src={chat.peer?.avatar_url || undefined} />
              </Indicator>
            )}
            <Box style={{ minWidth: 0, flex: 1 }}>
              <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                <Text size="md" fw={600} truncate lh={1.25}>
                  {title}
                </Text>
                {isExternal && chat.channel && <ChannelChip channel={chat.channel} />}
              </Group>
              {subtitle && (
                <Text size="xs" c="dimmed" truncate lh={1.25}>
                  {subtitle}
                </Text>
              )}
            </Box>
          </Group>

          <Group gap={6} wrap="nowrap" style={{ flex: "none" }} data-tn-density="dense">
            <Tooltip label={detailsLabel}>
              <ActionIcon
                variant={details.opened ? "light" : "subtle"}
                color={details.opened ? undefined : "gray"}
                size="input-sm"
                aria-label={detailsLabel}
                onClick={details.toggle}
              >
                <IconLayoutSidebarRight size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </PaneHeader>

        <ChatTimeline chat={chat} readSeq={readAtOpen.current ?? 0} typingNames={typing.map((x) => (x.contact ? title : (x.name ?? ""))).filter(Boolean)} />

        <ChatComposer key={chat.id} chat={chat} onTyping={notifyTyping} />
      </Box>

      <AsidePane
        opened={details.opened}
        onClose={details.close}
        compactQuery={NARROW_QUERY}
        resizable={{ storageKey: "chat-details", defaultWidth: 320, min: 260, max: 420 }}
      >
        {isExternal ? (
          <BusinessDetails chat={chat} onClose={details.close} onLeft={back} />
        ) : chat.mode === "group" ? (
          <GroupDetails chat={chat} onClose={details.close} onLeft={back} />
        ) : (
          <PeerDetails chat={chat} onClose={details.close} onOpenChat={onOpenChat} />
        )}
      </AsidePane>
    </Group>
  )
}

/** ChatTimeline 渲染聊天的消息流，支持按天分组、3 分钟同人连续消息合并、内部备注、跨语言译文与单条原文切换、送达状态、附件灯箱与正在输入提示。 */
function ChatTimeline({
  chat,
  readSeq,
  typingNames,
}: {
  chat: ConversationChatDTO
  readSeq: number
  typingNames: string[]
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const items = useTimeline(chat.id)
  const translation = useQuery(kit.queries.translation.settings())
  const translating = translation.data?.enabled ?? false
  const watch = useFillVisible(translating, me.data?.language)
  const [lightbox, setLightbox] = useState<LightboxImage | null>(null)
  const list = items.data ?? []
  const isExternal = chat.scope === "external"
  const stick = useStickToBottom({ ready: !items.isPending, list, myId: me.data?.id, typingCount: typingNames.length })

  const names = {
    member: (id: string) => (id === me.data?.id ? t("inbox.you") : (byId.get(id)?.name ?? t("event.someone"))),
    team: (id: string) => teams.find((x) => x.id === id)?.name ?? "",
  }

  const authorOf = (it: ConversationItemDTO): string => {
    if (it.actor.type === "contact") {
      return it.actor.name || chat.contact?.name || t("inbox.visitor")
    }
    if (it.actor.type === "member") {
      return names.member(it.actor.id)
    }
    return t("event.system")
  }

  if (items.isPending) {
    return (
      <Box className="inbox-timeline" px="var(--tn-pane-px)" py="md">
        <TimelineSkeleton />
      </Box>
    )
  }

  const firstNew = firstUnread(list, readSeq, me.data?.id)
  const bothMessages = (a: ConversationItemDTO, b: ConversationItemDTO) => a.kind === "message" && b.kind === "message"
  const memberOf = (memberId: string) =>
    byId.get(memberId) ??
    (memberId === me.data?.id ? me.data : undefined) ??
    chat.participants?.find((p) => p.type === "member" && p.id === memberId)

  return (
    <Box className="inbox-timeline-wrap">
      <Box ref={stick.scroller} className="inbox-timeline" onScroll={stick.onScroll}>
        <Stack gap={0} px="var(--tn-pane-px)" py="md">
          {list.length === 0 && (
            <Center py="xl">
              <Text size="sm" c="dimmed">
                {t("chat.start_hint")}
              </Text>
            </Center>
          )}
          {list.map((it, i) => {
            const prev = list[i - 1]
            const next = list[i + 1]
            const showDay = i === 0 || !sameDay(prev.created_at, it.created_at)
            const showNew = it === firstNew
            const groupedWithPrev = !showDay && !showNew && canGroupItems(prev, it, firstNew, bothMessages)
            const groupedWithNext = Boolean(next) && canGroupItems(it, next, firstNew, bothMessages)
            const gapTop = i === 0 ? 0 : groupedWithPrev ? 4 : 16
            const isOwn = it.actor.type === "member" && it.actor.id === me.data?.id
            const mine = isExternal ? it.actor.type !== "contact" : isOwn
            const author = authorOf(it)
            return (
              <Fragment key={it.id}>
                {showDay && <Divider my={i === 0 ? 4 : 12} label={dayLabel(it.created_at, ui, t, zone)} labelPosition="center" />}
                {showNew && <NewMessagesDivider />}
                {it.kind === "event" ? (
                  <Text size="xs" c="dimmed" ta="center" mt={gapTop} className="inbox-event">
                    {describeEvent(it, byId, names, t, ui)} · {clock(it.created_at, ui, zone)}
                  </Text>
                ) : it.kind === "note" ? (
                  <TimelineNote
                    conversationId={chat.id}
                    it={it}
                    author={author}
                    avatar={<ActorAvatar actor={it.actor} size={18} member={memberOf(it.actor.id)} fallbackName={author} />}
                    isOwn={isOwn}
                    time={clock(it.created_at, ui, zone)}
                    fullTime={fullDateTime(it.created_at, ui, zone)}
                    lang={me.data?.language}
                    translating={translating}
                    watch={watch}
                    mt={gapTop}
                    onPreview={setLightbox}
                  />
                ) : (
                  <ChatBubble
                    chat={chat}
                    it={it}
                    mine={mine}
                    isOwn={isOwn}
                    author={author}
                    avatar={
                      <ActorAvatar
                        actor={it.actor}
                        size={32}
                        member={it.actor.type === "member" ? memberOf(it.actor.id) : undefined}
                        contact={chat.contact}
                        fallbackName={author}
                      />
                    }
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

/** ChatBubble 渲染聊天中的单条气泡消息，自动展示跨语言译文并支持单条切换原文、发送译文展开以及外部渠道送达状态与失败重发。 */
function ChatBubble({
  chat,
  it,
  mine,
  isOwn,
  author,
  avatar,
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
  chat: ConversationChatDTO
  it: ConversationItemDTO
  mine: boolean
  isOwn: boolean
  author: string
  avatar: ReactNode
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
  const queryClient = useQueryClient()
  const [showOriginal, setShowOriginal] = useState(false)
  const [showSentTranslation, setShowSentTranslation] = useState(false)
  const [showVerified, setShowVerified] = useState(false)

  const [editing, setEditing] = useState(false)
  const [editText, setEditText] = useState("")
  const edit = useMutation({
    mutationFn: (text: string) => tenantApi.conversation.itemEdit(chat.id, it.id, { text }),
    onSuccess: () => {
      setEditing(false)
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
      void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "timeline", chat.id] })
    },
    onError: (err) => notifyError(err),
  })
  // 只编辑、撤回不对客户发出的内容：内部聊天的消息和内部备注；发给客户的消息要等更正流程
  const editable = isOwn && it.actor.type === "member" && (chat.scope === "internal" || it.kind === "note")
  const recalled = Boolean(it.recalled)
  const [recalling, setRecalling] = useState(false)
  const recall = useMutation({
    mutationFn: () => tenantApi.conversation.itemRecall(chat.id, it.id),
    onSuccess: () => {
      setRecalling(false)
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
      void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "timeline", chat.id] })
    },
    onError: (err) => notifyError(err),
  })

  const retry = useMutation({
    mutationFn: () => tenantApi.conversation.retry(chat.id, it.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
      void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "timeline", chat.id] })
    },
    onError: (err) => notifyError(err),
  })

  const isExternal = chat.scope === "external"
  const raw = it.content.text ?? ""
  const translatableForViewer = it.actor.type === "contact" || (it.actor.type === "member" && !isOwn)
  const translated = translatableForViewer ? translationOf(it, lang) : undefined
  const sentAs = isExternal && mine && it.content.original ? it.content.language : undefined
  const verified = !sentAs && isOwn ? verifiedTranslationOf(it) : undefined
  const shown = recalled
    ? t("inbox.message_recalled")
    : translated && !showOriginal
      ? translated
      : sentAs
        ? (it.content.original ?? "")
        : raw
  const fromLang = it.derived.language as string | undefined
  const needFill = translatableForViewer && translating && it.untranslated

  const hasCaption = Boolean(translated || needFill || sentAs || verified)
  const caption = hasCaption ? (
    <TranslationCaption
      translated={translated}
      needFill={needFill}
      fromLang={fromLang}
      showOriginal={showOriginal}
      onToggleOriginal={() => setShowOriginal((v) => !v)}
      sentAs={sentAs}
      showSent={showSentTranslation}
      onToggleSent={() => setShowSentTranslation((v) => !v)}
      verifiedLang={verified?.language}
      showVerified={showVerified}
      onToggleVerified={() => setShowVerified((v) => !v)}
    />
  ) : undefined

  const pendingAutoTranslate = Boolean(it.delivery === "sending" && it.derived?.pending_translation)
  const showDelivery = Boolean(isExternal && mine && it.delivery && (last || it.delivery === "failed" || it.delivery === "sending"))
  const deliveryNode =
    showDelivery && it.delivery ? (
      <>
        {pendingAutoTranslate && <Loader size={10} />}
        <Text span size="xs" c={deliveryColor[it.delivery]}>
          {pendingAutoTranslate ? t("inbox.delivery_translating") : t(`inbox.delivery_${it.delivery}` as const)}
        </Text>
        {it.delivery === "failed" && (
          <>
            <Text span size="xs" c="dimmed">
              ·
            </Text>
            <UnstyledButton fz="xs" c="red" td="underline" onClick={() => retry.mutate()} disabled={retry.isPending}>
              {t("inbox.retry")}
            </UnstyledButton>
          </>
        )}
      </>
    ) : undefined

  const expandedTranslation =
    sentAs && showSentTranslation ? { language: sentAs, text: raw } : verified && showVerified ? verified : undefined

  return (
    <>
    <MessageBubble
      conversationId={chat.id}
      attachments={recalled ? undefined : it.attachments}
      seq={it.seq}
      watchRef={needFill ? watch(it.id) : undefined}
      mine={mine}
      first={first}
      last={last}
      mt={mt}
      author={author}
      avatar={avatar}
      time={time}
      fullTime={fullTime}
      text={shown || undefined}
      mentions
      original={Boolean(translated && showOriginal)}
      expandedTranslation={expandedTranslation}
      caption={recalled ? undefined : caption}
      footerExtra={recalled ? undefined : deliveryNode}
      copyValue={recalled ? "" : undefined}
      edited={Boolean(it.revision) && !recalled}
      extraActions={
        editable && !recalled ? (
          <Group gap={2} wrap="nowrap">
            <Tooltip label={t("inbox.message_edit")}>
              <ActionIcon
                size="xs"
                variant="subtle"
                color="gray"
                aria-label={t("inbox.message_edit")}
                onClick={() => {
                  setEditText(it.content.text ?? "")
                  setEditing(true)
                }}
              >
                <IconPencil size={12} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label={t("inbox.message_recall")}>
              <ActionIcon
                size="xs"
                variant="subtle"
                color="gray"
                aria-label={t("inbox.message_recall")}
                onClick={() => setRecalling(true)}
              >
                <IconArrowBackUp size={12} />
              </ActionIcon>
            </Tooltip>
          </Group>
        ) : undefined
      }
      onPreview={onPreview}
    />
    <Modal opened={editing} onClose={() => setEditing(false)} title={t("inbox.message_edit_title")} centered>
      <Textarea
        value={editText}
        onChange={(e) => setEditText(e.currentTarget.value)}
        autosize
        minRows={3}
        maxRows={10}
        data-autofocus
      />
      <Group justify="flex-end" mt="md">
        <Button variant="default" onClick={() => setEditing(false)}>
          {t("inbox.cancel")}
        </Button>
        <Button onClick={() => edit.mutate(editText.trim())} loading={edit.isPending} disabled={!editText.trim()}>
          {t("inbox.message_edit_save")}
        </Button>
      </Group>
    </Modal>
    <Modal opened={recalling} onClose={() => setRecalling(false)} title={t("inbox.message_recall_title")} centered>
      <Text size="sm">{t("inbox.message_recall_confirm")}</Text>
      <Group justify="flex-end" mt="md">
        <Button variant="default" onClick={() => setRecalling(false)}>
          {t("inbox.cancel")}
        </Button>
        <Button color="red" onClick={() => recall.mutate()} loading={recall.isPending}>
          {t("inbox.message_recall")}
        </Button>
      </Group>
    </Modal>
    </>
  )
}

/** ChatComposer 是聊天底部的统一输入卡片：商务沟通支持“回复 / 内部备注”切换与对外自动翻译，支持 @ 提及、拖拽/粘贴附件、译文校对及快捷键发送。 */
function ChatComposer({ chat, onTyping }: { chat: ConversationChatDTO; onTyping: () => void }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const os = useOs()
  const isMac = os === "macos" || os === "ios"
  const modeKey = isMac ? "⌘I" : "Ctrl+I"
  const sendModKey = isMac ? "⌘ + ↵" : "Ctrl + Enter"
  const queryClient = useQueryClient()
  const me = useMe()
  const ctrlEnterSend = me.data?.send_shortcut === "ctrl_enter"
  const { list: allMembers, byId: membersById } = useMembers()

  const isExternal = chat.scope === "external"
  // 未发送的内容按聊天保存为草稿（与收件箱共用），切换聊天再回来不丢失
  const [replyMode, setReplyMode] = useState<DraftMode>(() => (isExternal ? (getDraft(chat.id)?.mode ?? "reply") : "reply"))
  const note = isExternal && replyMode === "note"

  const [text, setText] = useState(() => getDraft(chat.id)?.text ?? "")
  const [mentionIds, setMentionIds] = useState<string[]>(() => getDraft(chat.id)?.mentionIds ?? [])
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const area = useRef<HTMLTextAreaElement>(null)
  const resetFile = useRef<() => void>(null)

  useEffect(() => {
    saveDraft(chat.id, { text, mode: replyMode, mentionIds })
  }, [chat.id, text, replyMode, mentionIds])

  const queue = useUploadQueue<UploadedMedia>({
    upload: (file) => uploadMediaFile(file, (f) => tenantApi.file.upload({ file: f })),
    accept: ATTACHMENT_ACCEPT,
    maxFiles: MAX_ATTACHMENTS,
    maxSize: attachmentMaxSize,
    onError: (message) => notifications.show({ color: "red", message }),
  })
  const attachmentInputs = queue.ready.map(toAttachmentInput)
  const value = text.trim()
  const hasContent = Boolean(value) || attachmentInputs.length > 0

  // 内部聊天仅提示当前参与者；商务沟通的内部备注可 @ 全部同事，对外回复不触发内部 @ 列表
  const participantMemberIds = useMemo(() => {
    const ids = new Set<string>()
    for (const p of chat.participants ?? []) {
      if (p.type === "member" && p.id !== me.data?.id) ids.add(p.id)
    }
    if (ids.size === 0 && chat.peer?.type === "member" && chat.peer.id !== me.data?.id) {
      ids.add(chat.peer.id)
    }
    return ids
  }, [chat.participants, chat.peer, me.data?.id])

  const mentionCandidates = useMemo(() => {
    if (isExternal) {
      return note ? allMembers.filter((m) => m.id !== me.data?.id) : []
    }
    return allMembers.filter((m) => participantMemberIds.has(m.id))
  }, [allMembers, isExternal, me.data?.id, note, participantMemberIds])

  // 外部商务对外回复走客户/外部群语言；内部聊天和商务备注走统一的内部接收者推导（@ 提及同事 → 会话直接参与成员）
  const defaultTarget = useMemo(() => {
    if (isExternal && !note) {
      return chat.language ?? ""
    }
    const directLanguages: (string | undefined)[] = []
    if (!isExternal && chat.mode === "direct") {
      directLanguages.push(chat.peer?.language)
    }
    for (const p of chat.participants ?? []) {
      if (p.type === "member" && p.id !== me.data?.id) directLanguages.push(p.language)
    }
    return resolveInternalTargetLanguage({
      mentionIds,
      directLanguages,
      membersById,
      myId: me.data?.id,
      myLang: me.data?.language,
    })
  }, [chat.language, chat.mode, chat.participants, chat.peer?.language, isExternal, mentionIds, membersById, me.data?.id, me.data?.language, note])

  const preview = useComposerTranslation({
    conversationId: chat.id,
    text: value,
    defaultTarget,
    myLang: me.data?.language,
    mode: isExternal && !note ? "replace" : "verified",
    previewMode: isExternal && !note ? "outbound" : "internal",
    mentions: !isExternal || note ? mentionIds : undefined,
  })

  const send = useMutation({
    mutationFn: (vars: { translation?: ConversationTranslation; autoTranslate?: ConversationAutoTranslateRequest }) => {
      const ids = mentionCandidates.filter((m) => mentionIds.includes(m.id) && value.includes(`@${m.name}`)).map((m) => m.id)
      return tenantApi.chat.messageSend(chat.id, {
        text: value || undefined,
        mentions: ids.length > 0 ? ids : undefined,
        attachments: attachmentInputs.length > 0 ? attachmentInputs : undefined,
        note: note || undefined,
        translation: vars.translation,
        auto_translate: vars.autoTranslate,
      })
    },
    onSuccess: () => {
      clearDraft(chat.id)
      setText("")
      setMentionIds([])
      queue.clear()
      preview.reset()
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
      void queryClient.invalidateQueries({ queryKey: [...kit.queries.conversation.key(), "timeline", chat.id] })
    },
    onError: (err) => notifyError(err),
  })

  const submit = async (options?: { skipTranslation?: boolean }) => {
    if (!hasContent || queue.uploading || queue.hasError || send.isPending || preview.pending) return
    const prep = await preview.prepareSubmit({ skipTranslation: options?.skipTranslation })
    if (!prep.send) return
    send.mutate({ translation: prep.translation, autoTranslate: prep.autoTranslate })
  }

  const triggerRules = useMemo(
    () => [
      textTriggerRule({
        id: "mention",
        trigger: "@",
        enabled: mentionCandidates.length > 0,
        items: (q) => mentionCandidates.filter((m) => m.name.toLowerCase().includes(q.toLowerCase())).slice(0, 6),
        getKey: (m) => m.id,
        renderItem: (m: MemberTenantMember) => (
          <Group gap="xs" wrap="nowrap">
            <Avatar src={m.avatar_url || undefined} size={22} name={m.name} />
            <Text size="sm" truncate>
              {m.name}
            </Text>
            <Text size="xs" c="dimmed" ml="auto">
              {m.role_name}
            </Text>
          </Group>
        ),
        onSelect: (m, { replace }) => {
          setMentionIds((all) => (all.includes(m.id) ? all : [...all, m.id]))
          replace(`@${m.name} `)
        },
      }),
    ],
    [mentionCandidates],
  )

  const handleTextChange = useCallback(
    (next: string) => {
      setText(next)
      if (next.trim() && !note) onTyping()
    },
    [note, onTyping],
  )

  const trigger = useTextTrigger({
    ref: area,
    value: text,
    onChange: handleTextChange,
    rules: triggerRules,
  })

  const switchMode = useCallback(
    (nextMode: DraftMode) => {
      if (nextMode === replyMode) return
      setReplyMode(nextMode)
      preview.setTarget("")
      preview.reset()
      trigger.close()
    },
    [preview, replyMode, trigger],
  )

  const focusEnd = useCallback(() => {
    requestAnimationFrame(() => {
      const el = area.current
      if (!el) return
      el.focus()
      const end = el.value.length
      el.setSelectionRange(end, end)
    })
  }, [])

  // 非输入态按 R 聚焦回复，商务沟通中按 N 切到内部备注并聚焦到末尾
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return
      const key = e.key.toLowerCase()
      if (key === "r") {
        e.preventDefault()
        switchMode("reply")
        focusEnd()
      } else if (key === "n" && isExternal) {
        e.preventDefault()
        switchMode("note")
        focusEnd()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [focusEnd, isExternal, switchMode])

  const canSubmit = hasContent && !queue.uploading && !queue.hasError && !send.isPending && !preview.pending
  const sendHint = ctrlEnterSend ? t("inbox.ctrl_enter_hint", { key: sendModKey }) : t("inbox.enter_hint")
  const mainLabel = preview.confirming ? t("inbox.confirm_send") : note ? t("inbox.add_note") : t("inbox.send")

  const willAutoTranslateExternal =
    isExternal &&
    !note &&
    preview.enabled &&
    Boolean(preview.effectiveLang) &&
    (!me.data?.language || !sameLanguage(preview.effectiveLang, me.data.language))

  const placeholder = isExternal
    ? note
      ? t("inbox.note_placeholder")
      : willAutoTranslateExternal
        ? t("inbox.reply_placeholder_auto_translate", { lang: languageName(preview.effectiveLang, ui) })
        : t("chat.business_reply_placeholder")
    : t("chat.composer_placeholder")

  return (
    <Box
      className="inbox-composer"
      onDragEnter={(e) => {
        if (!e.dataTransfer.types.includes("Files")) return
        e.preventDefault()
        dragDepth.current++
        setDragging(true)
      }}
      onDragOver={(e) => {
        if (!e.dataTransfer.types.includes("Files")) return
        e.preventDefault()
      }}
      onDragLeave={() => {
        if (dragDepth.current === 0) return
        dragDepth.current--
        if (dragDepth.current === 0) setDragging(false)
      }}
      onDrop={(e) => {
        dragDepth.current = 0
        setDragging(false)
        queue.handleDrop(e)
      }}
    >
      <Box className="inbox-composer-box" data-note={note || undefined} data-dragging={dragging || undefined}>
        <TriggerMenu trigger={trigger} position="top" width="min(100%, 280px)" maxHeight={240} />
        {(isExternal || dragging) && (
          <Group gap="xs" className="inbox-composer-head" data-tn-density="inspector" wrap="wrap">
            {isExternal && (
              <Tooltip label={t("inbox.switch_mode_hint", { key: modeKey })} openDelay={350}>
                <SegmentedControl
                  value={replyMode}
                  onChange={(v) => switchMode(v as DraftMode)}
                  data={[
                    { value: "reply", label: t("inbox.mode_reply") },
                    { value: "note", label: t("inbox.mode_note") },
                  ]}
                />
              </Tooltip>
            )}
            {isExternal && note && (
              <Group gap={4} wrap="nowrap">
                <IconLock size={12} className="inbox-note-accent" style={{ flex: "none" }} />
                <Text size="xs" className="inbox-note-accent">
                  {t("chat.business_note_hint")}
                </Text>
              </Group>
            )}
            {dragging && (
              <Text size="xs" c="brand" fw={500} ml="auto">
                {t("inbox.attach_drop")}
              </Text>
            )}
          </Group>
        )}

        <AttachmentStrip items={queue.items} onRemove={queue.remove} onRetry={queue.retry} />

        <Box className="inbox-composer-main" data-split={preview.expanded || undefined}>
          <Box
            className="inbox-composer-body"
            onClick={(e) => {
              if (e.target === e.currentTarget) area.current?.focus()
            }}
          >
            <Textarea
              ref={area}
              variant="unstyled"
              size="md"
              classNames={{ input: "inbox-composer-input" }}
              value={text}
              onChange={(e) => trigger.handleChange(e.currentTarget.value, e.currentTarget.selectionStart)}
              onPaste={(e) => queue.handlePaste(e)}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing) return
                if (isExternal && (e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "i") {
                  e.preventDefault()
                  switchMode(replyMode === "reply" ? "note" : "reply")
                  return
                }
                if (trigger.handleKeyDown(e)) return
                const hitSend = ctrlEnterSend
                  ? e.key === "Enter" && (e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey
                  : e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey
                if (hitSend && canSubmit) {
                  e.preventDefault()
                  void submit()
                }
              }}
              placeholder={placeholder}
              autosize
              minRows={3}
              maxRows={10}
              maxLength={10000}
              aria-label={placeholder}
            />
          </Box>
          {preview.expanded && (
            <InlineTranslationPane
              hasText={Boolean(value)}
              myLang={me.data?.language}
              preview={preview}
              ctrlEnterSend={ctrlEnterSend}
              onSendWithoutTranslation={() => void submit({ skipTranslation: true })}
              onSubmit={() => void submit()}
            />
          )}
        </Box>

        <Group gap={6} className="inbox-composer-foot" data-tn-density="dense">
          <FileButton
            resetRef={resetFile}
            multiple
            accept={ATTACHMENT_ACCEPT}
            onChange={(files) => {
              if (files.length > 0) queue.add(files)
              resetFile.current?.()
            }}
          >
            {(props) => (
              <Tooltip label={t("inbox.attach")}>
                <ActionIcon {...props} variant="subtle" color="gray" size="input-sm" aria-label={t("inbox.attach")}>
                  <IconPaperclip size={16} />
                </ActionIcon>
              </Tooltip>
            )}
          </FileButton>

          {preview.confirming && (
            <Text size="xs" c="brand" ml={4}>
              {ctrlEnterSend ? t("inbox.ctrl_enter_again", { key: sendModKey }) : t("inbox.enter_again")}
            </Text>
          )}

          <ComposerSendArea
            preview={preview}
            myLang={me.data?.language}
            label={mainLabel}
            sendHint={sendHint}
            note={note}
            disabled={!canSubmit}
            loading={send.isPending || preview.pending}
            onSend={() => void submit()}
            menu={
              willAutoTranslateExternal && (
                <>
                  <Menu.Item onClick={() => void submit()}>{t("inbox.send")}</Menu.Item>
                  <Menu.Item onClick={() => void submit({ skipTranslation: true })}>{t("inbox.send_original")}</Menu.Item>
                </>
              )
            }
          />
        </Group>
      </Box>
    </Box>
  )
}
