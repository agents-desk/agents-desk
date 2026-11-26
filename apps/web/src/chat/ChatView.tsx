import type { ConversationChatDTO, ConversationItemDTO, ConversationTranslation, MemberTenantMember } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Anchor,
  Avatar,
  Box,
  Button,
  Center,
  CopyButton,
  Divider,
  FileButton,
  Group,
  Indicator,
  Loader,
  Paper,
  Skeleton,
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
  IconPaperclip,
  IconSend,
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
import {
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
import { languageName } from "../languages"
import { NARROW_QUERY } from "../layout"
import { chatDisplayName, useChat } from "./data"
import { GroupDetails } from "./GroupDetails"

/** LINK 匹配消息中的网址。 */
const LINK = /https?:\/\/[^\s<>\x22]+[^\s<>\x22\x27.,;:!?)\]}。，；：！？）】」』]/g

/**
 * ChatView 是单条内部聊天（单聊或群聊）的主对话区：
 * 顶栏展示对方资料/群成员数与群详情右侧栏开关；
 * 中间消息流支持跨语言自动互译（按阅读者语言默认展示译文并支持单条切换原文）、附件灯箱、@高亮和正在输入提示；
 * 底部输入框支持 @ 提及、拖拽/粘贴附件、统一译文校对展开栏及快捷键发送。
 */
export function ChatView({ id, tenantId, back }: { id: string; tenantId: string; back: () => void }): ReactNode {
  const t = useT()
  const narrow = useNarrow(NARROW_QUERY)
  const queryClient = useQueryClient()
  const me = useMe()
  const chatQuery = useChat(id)
  const timeline = useTimeline(id)
  const { typing, notifyTyping, clearTyping } = useConversationChannel(tenantId, id, me.data?.id)

  const details = useAsidePaneState("chat-group-details", { compactQuery: NARROW_QUERY })

  const chat = chatQuery.data
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

  if (!chat) {
    return (
      <Center style={{ flex: 1 }}>
        {chatQuery.isError ? <Text c="dimmed">{t("chat.not_found")}</Text> : <Loader size="sm" />}
      </Center>
    )
  }

  const title = chatDisplayName(chat, t)
  const subtitle =
    chat.mode === "group"
      ? t("chat.members_count", { n: chat.participant_count })
      : chat.peer?.status === "online"
        ? t("chat.status_online")
        : t("chat.status_away")

  return (
    <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
      <Box style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", height: "100%" }}>
        <PaneHeader fixed={!narrow} back={narrow ? back : undefined}>
          <Group gap="sm" wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
            {chat.mode === "group" ? (
              <ThemeIcon size={32} radius="xl" variant="light" color="brand" style={{ flex: "none" }}>
                <IconUsersGroup size={17} stroke={1.6} />
              </ThemeIcon>
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
              <Text size="md" fw={600} truncate lh={1.25}>
                {title}
              </Text>
              <Text size="xs" c="dimmed" truncate lh={1.25}>
                {subtitle}
              </Text>
            </Box>
          </Group>

          {chat.mode === "group" && (
            <Group gap={6} wrap="nowrap" style={{ flex: "none" }} data-tn-density="dense">
              <Tooltip label={t("chat.group_settings")}>
                <ActionIcon
                  variant={details.opened ? "light" : "subtle"}
                  color={details.opened ? undefined : "gray"}
                  size="input-sm"
                  aria-label={t("chat.group_settings")}
                  onClick={details.toggle}
                >
                  <IconLayoutSidebarRight size={16} />
                </ActionIcon>
              </Tooltip>
            </Group>
          )}
        </PaneHeader>

        <ChatTimeline chat={chat} typingNames={typing.map((x) => x.name ?? "").filter(Boolean)} />

        <ChatComposer key={chat.id} chat={chat} onTyping={notifyTyping} />
      </Box>

      {chat.mode === "group" && (
        <AsidePane
          opened={details.opened}
          onClose={details.close}
          compactQuery={NARROW_QUERY}
          resizable={{ storageKey: "chat-group-details", defaultWidth: 320, min: 280, max: 400 }}
        >
          <GroupDetails chat={chat} onClose={details.close} onLeft={back} />
        </AsidePane>
      )}
    </Group>
  )
}

/** ChatTimeline 渲染内部聊天的消息流，支持按天分组、跨语言译文与单条原文切换、附件灯箱与正在输入提示。 */
function ChatTimeline({
  chat,
  typingNames,
}: {
  chat: ConversationChatDTO
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
          <Skeleton h={48} w="52%" radius="md" />
          <Skeleton h={44} w="44%" ml="auto" radius="md" />
          <Skeleton h={56} w="60%" radius="md" />
        </Stack>
      </Box>
    )
  }

  return (
    <Box className="inbox-timeline-wrap">
      <Box ref={scroller} className="inbox-timeline">
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
            return (
              <Fragment key={it.id}>
                {showDay && <Divider my={i === 0 ? 4 : 12} label={dayLabel(it.created_at, ui, t, zone)} labelPosition="center" />}
                {it.kind === "event" ? (
                  <Text size="xs" c="dimmed" ta="center" mt={gapTop} className="inbox-event">
                    {describeEvent(it, byId, names, t, ui)} · {clock(it.created_at, ui, zone)}
                  </Text>
                ) : (
                  <ChatBubble
                    chatId={chat.id}
                    it={it}
                    isOwn={it.actor.type === "member" && it.actor.id === me.data?.id}
                    author={names.member(it.actor.id)}
                    avatarUrl={byId.get(it.actor.id)?.avatar_url || undefined}
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
      <ImageLightbox src={lightbox?.src ?? null} filename={lightbox?.filename} onClose={() => setLightbox(null)} />
    </Box>
  )
}

/** ChatBubble 渲染内部聊天中的单条气泡消息，自动展示跨语言译文并支持单条切换原文。 */
function ChatBubble({
  chatId,
  it,
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
  chatId: string
  it: ConversationItemDTO
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
  const [showOriginal, setShowOriginal] = useState(false)
  const [showVerified, setShowVerified] = useState(false)

  const raw = it.content.text ?? ""
  const translated = isOwn ? undefined : translationOf(it, lang)
  const verified = isOwn ? verifiedTranslationOf(it) : undefined
  const shown = translated && !showOriginal ? translated : raw
  const fromLang = it.derived.language as string | undefined
  const needFill = !isOwn && translating && it.untranslated

  return (
    <Box
      ref={needFill ? watch(it.id) : undefined}
      mt={mt}
      className="inbox-msg"
      style={{ alignSelf: isOwn ? "flex-end" : "flex-start", maxWidth: "min(74%, 640px)" }}
    >
      <Group gap={6} mb={3} px={4} justify={isOwn ? "flex-end" : "flex-start"} wrap="nowrap">
        {!isOwn && <Avatar size={18} radius="xl" name={author} color="initials" src={avatarUrl} />}
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

      <Group gap={6} wrap="nowrap" align="center" style={{ flexDirection: isOwn ? "row-reverse" : "row" }}>
        <Paper
          px={shown || showVerified ? 12 : 6}
          py={shown || showVerified ? 9 : 6}
          withBorder={!isOwn}
          bg={isOwn ? "var(--mantine-primary-color-light)" : undefined}
          className="inbox-bubble"
          data-mine={isOwn || undefined}
          data-incoming={!isOwn || undefined}
          data-first
          data-last
          data-original={(translated && showOriginal) || undefined}
          style={{ minWidth: 0 }}
        >
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
        </Paper>
        {shown && (
          <Box className="inbox-msg-actions" style={{ flex: "none" }}>
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

      {(translated || needFill || verified) && (
        <Group gap={4} mt={3} px={6} justify={isOwn ? "flex-end" : "flex-start"} wrap="wrap">
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
    </Box>
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

/** ChatComposer 是内部聊天底部的轻量化输入区，支持 @ 提及、拖拽/粘贴附件及快捷键发送。 */
function ChatComposer({ chat, onTyping }: { chat: ConversationChatDTO; onTyping: () => void }): ReactNode {
  const t = useT()
  const os = useOs()
  const isMac = os === "macos" || os === "ios"
  const sendModKey = isMac ? "⌘ + ↵" : "Ctrl + Enter"
  const queryClient = useQueryClient()
  const me = useMe()
  const ctrlEnterSend = me.data?.send_shortcut === "ctrl_enter"
  const { list: allMembers } = useMembers()

  const [text, setText] = useState("")
  const [mentionIds, setMentionIds] = useState<string[]>([])
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const area = useRef<HTMLTextAreaElement>(null)
  const resetFile = useRef<() => void>(null)

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

  // 群聊仅提示当前群内其他成员；单聊提示对方成员
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

  const mentionCandidates = useMemo(
    () => allMembers.filter((m) => participantMemberIds.has(m.id)),
    [allMembers, participantMemberIds],
  )

  const defaultTarget = useMemo(() => {
    if (chat.mode === "direct") {
      return defaultPeerLanguage([chat.peer?.language], me.data?.language)
    }
    return defaultPeerLanguage(
      (chat.participants ?? [])
        .filter((p) => !(p.type === "member" && p.id === me.data?.id))
        .map((p) => p.language),
      me.data?.language,
    )
  }, [chat.mode, chat.peer?.language, chat.participants, me.data?.id, me.data?.language])

  const preview = useComposerTranslation({
    conversationId: chat.id,
    text: value,
    defaultTarget,
    myLang: me.data?.language,
    mode: "verified",
  })

  const send = useMutation({
    mutationFn: (translation?: ConversationTranslation) => {
      const ids = mentionCandidates.filter((m) => mentionIds.includes(m.id) && value.includes(`@${m.name}`)).map((m) => m.id)
      return tenantApi.chat.messageSend(chat.id, {
        text: value || undefined,
        mentions: ids.length > 0 ? ids : undefined,
        attachments: attachmentInputs.length > 0 ? attachmentInputs : undefined,
        translation,
      })
    },
    onSuccess: () => {
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
    send.mutate(prep.translation)
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
      if (next.trim()) onTyping()
    },
    [onTyping],
  )

  const trigger = useTextTrigger({
    ref: area,
    value: text,
    onChange: handleTextChange,
    rules: triggerRules,
  })

  const canSubmit = hasContent && !queue.uploading && !queue.hasError && !send.isPending && !preview.pending
  const sendHint = ctrlEnterSend ? t("inbox.ctrl_enter_hint", { key: sendModKey }) : t("inbox.enter_hint")
  const mainLabel = preview.confirming ? t("inbox.confirm_send") : t("inbox.send")

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
      <Box className="inbox-composer-box" data-dragging={dragging || undefined}>
        <TriggerMenu trigger={trigger} position="top" width="min(100%, 280px)" maxHeight={240} />
        {dragging && (
          <Group className="inbox-composer-head">
            <Text size="xs" c="brand" fw={500}>
              {t("inbox.attach_drop")}
            </Text>
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
              placeholder={t("chat.composer_placeholder")}
              autosize
              minRows={2}
              maxRows={8}
              maxLength={10000}
              aria-label={t("chat.composer_placeholder")}
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

          <TranslationToggleButton preview={preview} myLang={me.data?.language} />

          {preview.confirming && (
            <Text size="xs" c="brand" ml={4}>
              {ctrlEnterSend ? t("inbox.ctrl_enter_again", { key: sendModKey }) : t("inbox.enter_again")}
            </Text>
          )}

          <Tooltip label={sendHint} openDelay={350}>
            <Button
              ml="auto"
              leftSection={<IconSend size={14} />}
              disabled={!canSubmit}
              loading={send.isPending || preview.pending}
              onClick={() => void submit()}
            >
              {mainLabel}
            </Button>
          </Tooltip>
        </Group>
      </Box>
    </Box>
  )
}
