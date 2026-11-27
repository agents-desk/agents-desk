import type {
  ConversationAutoTranslateRequest,
  ConversationChatDTO,
  ConversationItemDTO,
  ConversationTranslation,
  MemberTenantMember,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Anchor,
  Avatar,
  Box,
  Menu,
  Center,
  CopyButton,
  Divider,
  FileButton,
  Group,
  Indicator,
  Loader,
  Paper,
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
import {
  IconCheck,
  IconCopy,
  IconLanguage,
  IconLayoutSidebarRight,
  IconLock,
  IconPaperclip,
  IconUsersGroup,
} from "@tabler/icons-react"
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
import {
  ATTACHMENT_ACCEPT,
  AttachmentStrip,
  attachmentMaxSize,
  type LightboxImage,
  MAX_ATTACHMENTS,
  MessageAttachments,
  toAttachmentInput,
  type UploadedMedia,
  uploadMediaFile,
} from "../inbox/Attachments"
import { useConversationChannel } from "../inbox/channel"
import { channelColor, ChannelChip } from "../inbox/channel-badge"
import {
  ComposerSendArea,
  defaultPeerLanguage,
  InlineTranslationPane,
  TranslationToggleButton,
  useComposerTranslation,
  verifiedTranslationOf,
} from "../inbox/ComposerTranslation"
import { useMe, useMembers, useTeams, useTimeline } from "../inbox/data"
import { describeEvent } from "../inbox/events"
import { type FillWatcher, useFillVisible } from "../inbox/fill"
import { clock, dayLabel, fullDateTime, sameDay } from "../inbox/format"
import { translationOf } from "../inbox/Timeline"
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

/** LINK 匹配消息中的网址。 */
const LINK = /https?:\/\/[^\s<>\x22]+[^\s<>\x22\x27.,;:!?)\]}。，；：！？）】」』]/g

/** deliveryColor 是外部渠道送达状态的颜色：已读是进行中的颜色，失败是红色，其余是灰色。 */
const deliveryColor = { sending: "dimmed", delivered: "dimmed", read: "bright-blue", failed: "red" } as const

/**
 * ChatView 是单条聊天（内部单聊/群聊 或 商务沟通 1:1/外部群聊）的主对话区：
 * 顶栏展示对方资料/渠道徽章/群成员数与右侧详情栏开关；
 * 中间消息流支持跨语言自动互译（按阅读者语言默认展示译文并支持单条切换原文）、商务沟通内部备注、外部渠道送达状态与重发、附件灯箱、@高亮和正在输入提示；
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
    if (last?.kind === "message") {
      clearTyping(last.actor.id)
    }
  }, [last, clearTyping])

  // 页面在前台且出现更新的消息时自动推进已读位置
  const lastSeq = last?.seq ?? 0
  const marked = useRef(0)
  useEffect(() => {
    if (!chat || lastSeq <= Math.max(chat.read_seq, marked.current) || document.visibilityState !== "visible") return
    marked.current = lastSeq
    void tenantApi.conversation.read(id, { seq: lastSeq }).then(() => {
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
    })
  }, [chat, id, lastSeq, queryClient])

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
              <Avatar size={32} radius="xl" name={title} color="initials" style={{ flex: "none" }} />
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

/** ChatTimeline 渲染聊天的消息流，支持按天分组、内部备注、跨语言译文与单条原文切换、送达状态、附件灯箱与正在输入提示。 */
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
            const showDay = i === 0 || !sameDay(prev.created_at, it.created_at)
            const gapTop = i === 0 ? 0 : 12
            const isOwn = it.actor.type === "member" && it.actor.id === me.data?.id
            const mine = isExternal ? it.actor.type !== "contact" : isOwn
            return (
              <Fragment key={it.id}>
                {showDay && <Divider my={i === 0 ? 4 : 12} label={dayLabel(it.created_at, ui, t, zone)} labelPosition="center" />}
                {it === firstNew && <NewMessagesDivider />}
                {it.kind === "event" ? (
                  <Text size="xs" c="dimmed" ta="center" mt={gapTop} className="inbox-event">
                    {describeEvent(it, byId, names, t, ui)} · {clock(it.created_at, ui, zone)}
                  </Text>
                ) : it.kind === "note" ? (
                  <ChatNote
                    chatId={chat.id}
                    it={it}
                    author={authorOf(it)}
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
                    author={authorOf(it)}
                    avatarUrl={it.actor.type === "member" ? byId.get(it.actor.id)?.avatar_url || undefined : undefined}
                    time={clock(it.created_at, ui, zone)}
                    fullTime={fullDateTime(it.created_at, ui, zone)}
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
  avatarUrl,
  time,
  fullTime,
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
  avatarUrl?: string
  time: string
  fullTime: string
  lang: string | undefined
  translating: boolean
  watch: FillWatcher
  mt: number
  onPreview: (img: LightboxImage) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const queryClient = useQueryClient()
  const [showOriginal, setShowOriginal] = useState(false)
  const [showSentTranslation, setShowSentTranslation] = useState(false)
  const [showVerified, setShowVerified] = useState(false)

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
  const shown = translated && !showOriginal ? translated : sentAs ? (it.content.original ?? "") : raw
  const fromLang = it.derived.language as string | undefined
  const needFill = translatableForViewer && translating && it.untranslated
  const showDelivery = Boolean(isExternal && mine && it.delivery)

  return (
    <Box
      ref={needFill ? watch(it.id) : undefined}
      mt={mt}
      className="inbox-msg"
      style={{ alignSelf: mine ? "flex-end" : "flex-start", maxWidth: "min(74%, 640px)" }}
    >
      <Group gap={6} mb={3} px={4} justify={mine ? "flex-end" : "flex-start"} wrap="nowrap">
        {!mine && <Avatar size={18} radius="xl" name={author} color="initials" src={avatarUrl} />}
        <Text size="xs" c="dimmed">
          {author}
        </Text>
        <Text size="xs" c="dimmed">
          ·
        </Text>
        <Tooltip label={fullTime} openDelay={300}>
          <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
            {time}
          </Text>
        </Tooltip>
      </Group>

      <Group gap={6} wrap="nowrap" align="center" style={{ flexDirection: mine ? "row-reverse" : "row" }}>
        <Paper
          px={shown || showVerified || showSentTranslation ? 12 : 6}
          py={shown || showVerified || showSentTranslation ? 9 : 6}
          withBorder={!mine}
          bg={mine ? "var(--mantine-primary-color-light)" : undefined}
          className="inbox-bubble"
          data-mine={mine || undefined}
          data-incoming={!mine || undefined}
          data-first
          data-last
          data-original={(translated && showOriginal) || undefined}
          style={{ minWidth: 0 }}
        >
          {shown && <ChatText text={shown} />}
          <MessageAttachments conversationId={chat.id} attachments={it.attachments} hasText={Boolean(shown)} onPreview={onPreview} />
          {sentAs && showSentTranslation && (
            <Box mt={6} pt={6} className="inbox-sent-translation">
              <Text size="xs" c="dimmed">
                {t("inbox.sent_translation", { lang: languageName(sentAs, ui) })}
              </Text>
              <ChatText text={raw} />
            </Box>
          )}
          {verified && showVerified && (
            <Box mt={6} pt={6} className="inbox-sent-translation">
              <Text size="xs" c="dimmed">
                {t("inbox.sent_translation", { lang: languageName(verified.language, ui) })}
              </Text>
              <ChatText text={verified.text} />
            </Box>
          )}
        </Paper>
        {shown && (
          <Box className="inbox-msg-actions" style={{ flex: "none" }}>
            <CopyButton value={shown} timeout={1500}>
              {({ copied, copy }) => (
                <Tooltip label={t(copied ? "inbox.copied" : "inbox.copy_message")} openDelay={250}>
                  <ActionIcon variant="subtle" color={copied ? "green" : "gray"} size={22} onClick={copy} aria-label={t("inbox.copy_message")}>
                    {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                  </ActionIcon>
                </Tooltip>
              )}
            </CopyButton>
          </Box>
        )}
      </Group>

      {(translated || needFill || sentAs || verified || showDelivery) && (
        <Group gap={4} mt={3} px={6} justify={mine ? "flex-end" : "flex-start"} wrap="wrap">
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
              <UnstyledButton className="inbox-caption-btn" onClick={() => setShowOriginal((v) => !v)}>
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
          ) : sentAs ? (
            <>
              <IconLanguage size={12} className="inbox-caption-icon" />
              <Text size="xs" c="dimmed">
                {t("inbox.sent_as", { lang: languageName(sentAs, ui) })}
              </Text>
              <Text size="xs" c="dimmed">
                ·
              </Text>
              <UnstyledButton className="inbox-caption-btn" onClick={() => setShowSentTranslation((v) => !v)}>
                {t(showSentTranslation ? "inbox.hide_translation" : "inbox.view_translation")}
              </UnstyledButton>
            </>
          ) : verified ? (
            <>
              <IconLanguage size={12} className="inbox-caption-icon" />
              <Text size="xs" c="dimmed">
                {t("inbox.sent_as", { lang: languageName(verified.language, ui) })}
              </Text>
              <Text size="xs" c="dimmed">
                ·
              </Text>
              <UnstyledButton className="inbox-caption-btn" onClick={() => setShowVerified((v) => !v)}>
                {t(showVerified ? "inbox.hide_translation" : "inbox.view_translation")}
              </UnstyledButton>
            </>
          ) : null}

          {showDelivery && it.delivery && (
            <>
              {(translated || needFill || sentAs || verified) && (
                <Text size="xs" c="dimmed">
                  ·
                </Text>
              )}
              {it.delivery === "sending" && Boolean(it.derived?.pending_translation) && <Loader size={10} />}
              <Text span size="xs" c={deliveryColor[it.delivery]}>
                {it.delivery === "sending" && Boolean(it.derived?.pending_translation)
                  ? t("inbox.delivery_translating")
                  : t(`inbox.delivery_${it.delivery}` as const)}
              </Text>
              {it.delivery === "failed" && (
                <UnstyledButton className="inbox-caption-btn" onClick={() => retry.mutate()} disabled={retry.isPending}>
                  {t("inbox.retry")}
                </UnstyledButton>
              )}
            </>
          )}
        </Group>
      )}
    </Box>
  )
}

/** ChatNote 渲染商务沟通中的内部备注卡片：淡黄底加锁图标，仅团队成员可见，支持跨语言译文与原文切换。 */
function ChatNote({
  chatId,
  it,
  author,
  isOwn,
  time,
  fullTime,
  lang,
  translating,
  watch,
  mt,
  onPreview,
}: {
  chatId: string
  it: ConversationItemDTO
  author: string
  isOwn: boolean
  time: string
  fullTime: string
  lang: string | undefined
  translating: boolean
  watch: FillWatcher
  mt: number
  onPreview: (img: LightboxImage) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [showOriginal, setShowOriginal] = useState(false)
  const [showVerified, setShowVerified] = useState(false)

  const raw = it.content.text ?? ""
  const translated = isOwn ? undefined : translationOf(it, lang)
  const verified = isOwn ? verifiedTranslationOf(it) : undefined
  const shown = translated && !showOriginal ? translated : raw
  const fromLang = it.derived.language as string | undefined
  const needFill = !isOwn && translating && it.untranslated

  return (
    <Paper
      ref={needFill ? watch(it.id) : undefined}
      mt={mt}
      px="sm"
      py={8}
      radius="md"
      bg="var(--mantine-color-yellow-light)"
      className="inbox-note"
    >
      <Group gap={6} mb={3} wrap="nowrap">
        <IconLock size={12} className="inbox-note-accent" style={{ flex: "none" }} />
        <Text size="xs" fw={600} className="inbox-note-accent">
          {t("inbox.note_by", { who: author })}
        </Text>
        <Text size="xs" className="inbox-note-accent" opacity={0.75}>
          ·
        </Text>
        <Tooltip label={fullTime} openDelay={300}>
          <Text size="xs" className="inbox-note-accent" opacity={0.85} style={{ fontVariantNumeric: "tabular-nums" }}>
            {time}
          </Text>
        </Tooltip>
        {shown && (
          <Box ml="auto" className="inbox-msg-actions">
            <CopyButton value={shown} timeout={1500}>
              {({ copied, copy }) => (
                <Tooltip label={t(copied ? "inbox.copied" : "inbox.copy_message")} openDelay={250}>
                  <ActionIcon variant="subtle" color={copied ? "green" : "gray"} size={22} onClick={copy} aria-label={t("inbox.copy_message")}>
                    {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                  </ActionIcon>
                </Tooltip>
              )}
            </CopyButton>
          </Box>
        )}
      </Group>

      {shown && <ChatText text={shown} />}
      <MessageAttachments conversationId={chatId} attachments={it.attachments} hasText={Boolean(shown)} onPreview={onPreview} />

      {verified && showVerified && (
        <Box mt={6} pt={6} className="inbox-sent-translation">
          <Text size="xs" c="dimmed">
            {t("inbox.sent_translation", { lang: languageName(verified.language, ui) })}
          </Text>
          <ChatText text={verified.text} />
        </Box>
      )}

      {(translated || needFill || verified) && (
        <Group gap={4} mt={4} wrap="wrap">
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
              <UnstyledButton className="inbox-caption-btn" onClick={() => setShowOriginal((v) => !v)}>
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
          ) : verified ? (
            <>
              <IconLanguage size={12} className="inbox-caption-icon" />
              <Text size="xs" c="dimmed">
                {t("inbox.sent_as", { lang: languageName(verified.language, ui) })}
              </Text>
              <Text size="xs" c="dimmed">
                ·
              </Text>
              <UnstyledButton className="inbox-caption-btn" onClick={() => setShowVerified((v) => !v)}>
                {t(showVerified ? "inbox.hide_translation" : "inbox.view_translation")}
              </UnstyledButton>
            </>
          ) : null}
        </Group>
      )}
    </Paper>
  )
}

/** ChatText 渲染聊天正文，自动识别链接并加粗 @ 提及。 */
function ChatText({ text }: { text: string }): ReactNode {
  const parts: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(LINK)) {
    const at = m.index ?? 0
    if (at > last) parts.push(<MentionText key={`t:${last}`} text={text.slice(last, at)} />)
    parts.push(
      <Anchor key={`a:${at}`} href={m[0]} target="_blank" rel="noopener noreferrer" size="md">
        {m[0]}
      </Anchor>,
    )
    last = at + m[0].length
  }
  if (last < text.length) parts.push(<MentionText key={`t:${last}`} text={text.slice(last)} />)
  return (
    <Text size="md" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
      {parts}
    </Text>
  )
}

/** MentionText 高亮片段中的 @同事。 */
function MentionText({ text }: { text: string }): ReactNode {
  return (
    <>
      {text.split(/(@\S+)/).map((part, i) =>
        part.startsWith("@") ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: 顺序固定
          <Text key={i} span fw={700} c="brand">
            {part}
          </Text>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: 顺序固定
          <Fragment key={i}>{part}</Fragment>
        ),
      )}
    </>
  )
}

/** ChatComposer 是聊天底部的轻量化输入区：商务沟通支持“回复 / 内部备注”切换与对外自动翻译，支持 @ 提及、拖拽/粘贴附件及快捷键发送。 */
function ChatComposer({ chat, onTyping }: { chat: ConversationChatDTO; onTyping: () => void }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const os = useOs()
  const isMac = os === "macos" || os === "ios"
  const sendModKey = isMac ? "⌘ + ↵" : "Ctrl + Enter"
  const queryClient = useQueryClient()
  const me = useMe()
  const ctrlEnterSend = me.data?.send_shortcut === "ctrl_enter"
  const { list: allMembers } = useMembers()

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

  // 非输入态按 R 聚焦回复，商务沟通中按 N 切到内部备注并聚焦
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return
      const key = e.key.toLowerCase()
      if (key === "r") {
        e.preventDefault()
        setReplyMode("reply")
        area.current?.focus()
      } else if (key === "n" && isExternal) {
        e.preventDefault()
        setReplyMode("note")
        area.current?.focus()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [isExternal])

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

  const defaultTarget = useMemo(() => {
    if (isExternal && !note) {
      return chat.language ?? ""
    }
    if (!isExternal && chat.mode === "direct") {
      return defaultPeerLanguage([chat.peer?.language], me.data?.language)
    }
    return defaultPeerLanguage(
      (chat.participants ?? [])
        .filter((p) => p.type === "member" && p.id !== me.data?.id)
        .map((p) => p.language),
      me.data?.language,
    )
  }, [chat.language, chat.mode, chat.participants, chat.peer?.language, isExternal, me.data?.id, me.data?.language, note])

  const preview = useComposerTranslation({
    conversationId: chat.id,
    text: value,
    defaultTarget,
    myLang: me.data?.language,
    mode: isExternal && !note ? "replace" : "verified",
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
          <Group className="inbox-composer-head" justify="space-between" gap="xs" wrap="nowrap">
            {isExternal ? (
              <SegmentedControl
                size="xs"
                value={replyMode}
                onChange={(v) => setReplyMode(v as DraftMode)}
                data={[
                  { value: "reply", label: t("inbox.mode_reply") },
                  {
                    value: "note",
                    label: (
                      <Group gap={4} wrap="nowrap">
                        <IconLock size={12} />
                        <span>{t("inbox.mode_note")}</span>
                      </Group>
                    ),
                  },
                ]}
              />
            ) : (
              <Box />
            )}
            {dragging ? (
              <Text size="xs" c="brand" fw={500}>
                {t("inbox.attach_drop")}
              </Text>
            ) : isExternal && note ? (
              <Text size="xs" c="dimmed" truncate>
                {t("chat.business_note_hint")}
              </Text>
            ) : null}
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
              minRows={2}
              maxRows={8}
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

          <TranslationToggleButton preview={preview} myLang={me.data?.language} note={note} />

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
