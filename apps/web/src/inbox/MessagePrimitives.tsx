import type { ConversationActor, ConversationItemDTO } from "@agents-desk/client/tenant"
import { ActionIcon, Anchor, Avatar, Box, Button, CopyButton, Group, Loader, Modal, Paper, Text, Textarea, ThemeIcon, Tooltip, UnstyledButton } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconArrowBackUp, IconCheck, IconCopy, IconInfoCircle, IconLanguage, IconLock, IconPencil, IconRobot } from "@tabler/icons-react"
import { notifyError, useLanguage, useT } from "@go-tenon/ui"
import { Fragment, type ReactNode, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { ContactAvatar, type ContactLabel } from "../contacts/display"
import { languageName } from "../languages"
import { type LightboxImage, MessageAttachments } from "./Attachments"
import { verifiedTranslationOf } from "./ComposerTranslation"
import type { FillWatcher } from "./fill"
import { sameDay } from "./format"
import { HighlightQuery } from "./Highlight"

/** GROUP_WINDOW_MS 是同一发送者连续消息合并为同一组的间隔上限（3 分钟）。 */
const GROUP_WINDOW_MS = 3 * 60 * 1000

/** LINK 匹配消息中的网址：http:// 或 https:// 开头，到空白为止，去掉结尾标点。 */
const LINK = /https?:\/\/[^\s<>\x22]+[^\s<>\x22\x27.,;:!?)\]}。，；：！？）】」』]/g

/** deliveryColor 是外部渠道送达状态的颜色：已读为进行中蓝，失败为红色，其余为灰色。 */
export const deliveryColor = { sending: "dimmed", delivered: "dimmed", read: "bright-blue", failed: "red" } as const

/** translationOf 返回他人的消息在目标语言 lang 中的译文；缺少译文或同语言时返回 undefined。 */
export function translationOf(it: ConversationItemDTO, lang: string | undefined): string | undefined {
  const all = it.derived.translations as Record<string, string> | undefined
  return lang ? all?.[lang] : undefined
}

/**
 * canGroupItems 判断相邻两条时间线项是否合并为同人连续消息组：
 * 要求两条均为消息或备注、同发送者、同自然日、间隔不超过 3 分钟、满足自定义分桶条件，且在“新消息”分隔起点处断开分组。
 */
export function canGroupItems(
  prev: ConversationItemDTO | undefined,
  cur: ConversationItemDTO,
  firstNew: ConversationItemDTO | undefined,
  sameBucket?: (a: ConversationItemDTO, b: ConversationItemDTO) => boolean,
): boolean {
  if (!prev || prev.kind === "event" || cur.kind === "event") return false
  if (cur === firstNew) return false
  if (prev.actor.type !== cur.actor.type || prev.actor.id !== cur.actor.id) return false
  if (sameBucket && !sameBucket(prev, cur)) return false
  if (!sameDay(prev.created_at, cur.created_at)) return false
  return Math.abs(new Date(cur.created_at).getTime() - new Date(prev.created_at).getTime()) <= GROUP_WINDOW_MS
}

/**
 * useSyncRead 在页面处于前台且出现更新的时间线项时推进已读位置，并在标签页切回前台时补标已读。
 */
export function useSyncRead({
  ready,
  readSeq,
  lastSeq,
  onMark,
}: {
  ready: boolean
  readSeq: number
  lastSeq: number
  onMark: (seq: number) => void
}): void {
  const marked = useRef(0)
  const onMarkRef = useRef(onMark)
  onMarkRef.current = onMark

  useEffect(() => {
    const syncRead = () => {
      if (!ready || lastSeq <= Math.max(readSeq, marked.current) || document.visibilityState !== "visible") return
      marked.current = lastSeq
      onMarkRef.current(lastSeq)
    }
    syncRead()
    document.addEventListener("visibilitychange", syncRead)
    return () => document.removeEventListener("visibilitychange", syncRead)
  }, [ready, readSeq, lastSeq])
}

/** CopyAction 是消息与备注旁的轻量一键复制按钮，复制成功后短暂显示绿色对勾。 */
export function CopyAction({ value }: { value: string }): ReactNode {
  const t = useT()
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={t(copied ? "inbox.copied" : "inbox.copy_message")} openDelay={250}>
          <ActionIcon
            variant="subtle"
            color={copied ? "green" : "gray"}
            size={22}
            onClick={copy}
            aria-label={t("inbox.copy_message")}
          >
            {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  )
}

/** CaptionToggle 是消息下方的轻量译文切换按钮，悬停时呈现品牌色。 */
export function CaptionToggle({ label, onClick }: { label: string; onClick: () => void }): ReactNode {
  return (
    <UnstyledButton className="inbox-caption-btn" onClick={onClick}>
      {label}
    </UnstyledButton>
  )
}

/**
 * MessageText 渲染消息与备注正文：自动识别网址为新标签页链接，按需高亮 @提及 和搜索关键词。
 */
export function MessageText({
  text,
  highlight,
  mentions,
  mentionClassName,
}: {
  text: string
  highlight?: string
  mentions?: boolean
  mentionClassName?: string
}): ReactNode {
  const renderSegment = (segment: string, keyPrefix: string): ReactNode => {
    if (!mentions) return <HighlightQuery text={segment} query={highlight} />
    return segment.split(/(@\S+)/).map((part, i) =>
      part.startsWith("@") ? (
        // biome-ignore lint/suspicious/noArrayIndexKey: 拆分出的片段顺序固定
        <Text key={`${keyPrefix}:m:${i}`} span fw={700} c={mentionClassName ? undefined : "brand"} className={mentionClassName}>
          <HighlightQuery text={part} query={highlight} />
        </Text>
      ) : (
        // biome-ignore lint/suspicious/noArrayIndexKey: 拆分出的片段顺序固定
        <Fragment key={`${keyPrefix}:p:${i}`}>
          <HighlightQuery text={part} query={highlight} />
        </Fragment>
      ),
    )
  }

  const parts: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(LINK)) {
    const at = m.index ?? 0
    if (at > last) {
      parts.push(<Fragment key={`t:${last}`}>{renderSegment(text.slice(last, at), `t:${last}`)}</Fragment>)
    }
    parts.push(
      <Anchor key={`a:${at}`} href={m[0]} target="_blank" rel="noopener noreferrer" size="md">
        <HighlightQuery text={m[0]} query={highlight} />
      </Anchor>,
    )
    last = at + m[0].length
  }
  if (last < text.length) {
    parts.push(<Fragment key={`t:${last}`}>{renderSegment(text.slice(last), `t:${last}`)}</Fragment>)
  }

  return (
    <Text size="md" lh={1.5} style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
      {parts}
    </Text>
  )
}

/**
 * TranslationCaption 渲染消息或备注底部的翻译状态条：
 * 统一在左侧展示操作按钮（查看原文/译文），右侧紧跟源语言或目标语言说明；缺译文时展示补译中状态。
 */
export function TranslationCaption({
  translated,
  needFill,
  fromLang,
  showOriginal,
  onToggleOriginal,
  sentAs,
  showSent,
  onToggleSent,
  verifiedLang,
  showVerified,
  onToggleVerified,
}: {
  translated?: string
  needFill?: boolean
  fromLang?: string
  showOriginal?: boolean
  onToggleOriginal?: () => void
  sentAs?: string
  showSent?: boolean
  onToggleSent?: () => void
  verifiedLang?: string
  showVerified?: boolean
  onToggleVerified?: () => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()

  if (translated && onToggleOriginal) {
    return (
      <>
        <IconLanguage size={12} className="inbox-caption-icon" />
        <CaptionToggle label={t(showOriginal ? "inbox.view_translation" : "inbox.view_original")} onClick={onToggleOriginal} />
        {fromLang && (
          <>
            <Text size="xs" c="dimmed">
              ·
            </Text>
            <Text size="xs" c="dimmed">
              {languageName(fromLang, ui)}
            </Text>
          </>
        )}
      </>
    )
  }
  if (needFill) {
    return (
      <>
        <Loader size={10} />
        <Text size="xs" c="dimmed">
          {t("inbox.translating")}
        </Text>
      </>
    )
  }
  if (sentAs && onToggleSent) {
    return (
      <>
        <IconLanguage size={12} className="inbox-caption-icon" />
        <CaptionToggle label={t(showSent ? "inbox.hide_translation" : "inbox.view_translation")} onClick={onToggleSent} />
        <Text size="xs" c="dimmed">
          ·
        </Text>
        <Text size="xs" c="dimmed">
          {t("inbox.sent_as", { lang: languageName(sentAs, ui) })}
        </Text>
      </>
    )
  }
  if (verifiedLang && onToggleVerified) {
    return (
      <>
        <IconLanguage size={12} className="inbox-caption-icon" />
        <CaptionToggle label={t(showVerified ? "inbox.hide_translation" : "inbox.view_translation")} onClick={onToggleVerified} />
        <Text size="xs" c="dimmed">
          ·
        </Text>
        <Text size="xs" c="dimmed">
          {t("inbox.sent_as", { lang: languageName(verifiedLang, ui) })}
        </Text>
      </>
    )
  }
  return null
}

/**
 * ActorAvatar 统一渲染时间线发送者头像：
 * 成员优先展示头像图片，无图时按真实姓名生成稳定色首字头像；
 * 联系人复用全站统一的 ContactAvatar；机器人与系统消息使用语义图标头像区分。
 */
export function ActorAvatar({
  actor,
  size,
  member,
  contact,
  fallbackName,
}: {
  actor: ConversationActor
  size: number
  member?: { name?: string; avatar_url?: string }
  contact?: ContactLabel | null
  fallbackName?: string
}): ReactNode {
  if (actor.type === "contact" || actor.type === "external_user") {
    const target: ContactLabel =
      actor.type === "contact" && contact && (!contact.id || contact.id === actor.id)
        ? contact
        : { id: actor.id, name: actor.name || fallbackName }
    return <ContactAvatar contact={target} size={size} />
  }
  if (actor.type === "bot") {
    return (
      <ThemeIcon size={size} radius="xl" variant="light" color="brand" style={{ flex: "none" }}>
        <IconRobot size={Math.round(size * 0.56)} stroke={1.6} />
      </ThemeIcon>
    )
  }
  if (actor.type === "system") {
    return (
      <ThemeIcon size={size} radius="xl" variant="light" color="gray" style={{ flex: "none" }}>
        <IconInfoCircle size={Math.round(size * 0.56)} stroke={1.6} />
      </ThemeIcon>
    )
  }
  const displayName = member?.name?.trim() || actor.name?.trim() || fallbackName || "?"
  return <Avatar size={size} radius="xl" name={displayName} color="initials" src={member?.avatar_url || undefined} style={{ flex: "none" }} />
}

/** MessageBubbleProps 是统一聊天气泡的显示参数。 */
export interface MessageBubbleProps {
  conversationId: string
  itemId?: string
  attachments?: ConversationItemDTO["attachments"]
  seq?: number
  watchRef?: (el: HTMLElement | null) => void
  mine: boolean // 己方消息居右，对方或同事消息居左
  first: boolean // 连续同人消息组首条：展示头像与作者/时间头部
  last: boolean // 连续同人消息组末条：控制底部圆角
  mt: number
  author: string
  avatar: ReactNode // 32px 外侧圆形头像，组首显示，连续后续条自动占位对齐
  headerExtra?: ReactNode // 组首头部附加标签（渠道胶囊、公开进度标记等）
  edited?: boolean // 编辑过的消息在时间旁标注“已编辑”
  corrected?: boolean // 已被更正的原消息：降权展示并标注“已更正”
  onViewCorrection?: () => void // 点击“查看更正”跳转到最新更正消息
  correction?: { text?: string; onJump?: () => void } // 作为更正发出的消息顶部引用原消息
  time: string
  fullTime: string
  text?: string
  copyValue?: string
  highlight?: string
  mentions?: boolean
  focused?: boolean
  original?: boolean // 展示原文时启用略沉底色
  publicUpdate?: boolean // 工单公开进度气泡样式标记
  expandedTranslation?: { language: string; text: string }
  quote?: { author: string; text: string } // 被引用回复的消息摘要
  extraActions?: ReactNode // 气泡旁悬停附加按钮（如群聊引用回复）
  caption?: ReactNode
  footerExtra?: ReactNode
  onPreview: (img: LightboxImage) => void
}

/**
 * MessageBubble 是会话、聊天与工单三处共用的聊天气泡组件：
 * 头像与气泡本体同行并顶边齐平（连续后续条占位 32px 保持边线笔直），
 * 作者与时间行、底部译文与状态行水平跳过 42px 头像列，与气泡外边缘对齐。
 */
export function MessageBubble({
  conversationId,
  itemId,
  attachments,
  seq,
  watchRef,
  mine,
  first,
  last,
  mt,
  author,
  avatar,
  headerExtra,
  edited,
  corrected,
  onViewCorrection,
  correction,
  time,
  fullTime,
  text,
  copyValue,
  highlight,
  mentions,
  focused,
  original,
  publicUpdate,
  expandedTranslation,
  quote,
  extraActions,
  caption,
  footerExtra,
  onPreview,
}: MessageBubbleProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const copyText = copyValue ?? text
  const hasBodyText = Boolean(text) || Boolean(expandedTranslation) || Boolean(quote) || Boolean(correction)
  const correctedFooter = corrected ? (
    <>
      <Text span size="xs" c="deep-orange">
        {t("inbox.message_corrected")}
      </Text>
      {onViewCorrection && (
        <>
          <Text span size="xs" c="dimmed">
            ·
          </Text>
          <CaptionToggle label={t("inbox.view_correction")} onClick={onViewCorrection} />
        </>
      )}
    </>
  ) : undefined

  return (
    <Box
      ref={watchRef}
      mt={mt}
      data-seq={seq}
      data-item-id={itemId}
      className="inbox-msg"
      style={{
        alignSelf: mine ? "flex-end" : "flex-start",
        maxWidth: "min(76%, 660px)",
        display: "flex",
        flexDirection: "column",
        alignItems: mine ? "flex-end" : "flex-start",
      }}
    >
      {first && (
        <Group
          gap={6}
          mb={4}
          pl={mine ? 0 : 42}
          pr={mine ? 42 : 0}
          justify={mine ? "flex-end" : "flex-start"}
          wrap="nowrap"
          align="center"
          style={{ maxWidth: "100%" }}
        >
          <Text size="xs" fw={500} c="dimmed" truncate lh={1.2}>
            {author}
          </Text>
          {headerExtra}
          <Tooltip label={fullTime} openDelay={300}>
            <Text fz={11} c="dimmed" opacity={0.78} lh={1.2} style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
              {time}
            </Text>
          </Tooltip>
          {edited && (
            <Text fz={11} c="dimmed" lh={1.2} style={{ flex: "none" }}>
              {t("inbox.message_edited")}
            </Text>
          )}
        </Group>
      )}

      <Group
        gap={10}
        wrap="nowrap"
        align="flex-start"
        style={{ flexDirection: mine ? "row-reverse" : "row", maxWidth: "100%" }}
      >
        {first ? (
          <Box mt={3} style={{ flex: "none" }}>
            {avatar}
          </Box>
        ) : (
          <Box w={32} style={{ flex: "none" }} aria-hidden="true" />
        )}

        <Box className="inbox-bubble-wrap">
          <Paper
            px={hasBodyText ? 13 : 6}
            py={hasBodyText ? 8 : 6}
            className={focused ? "inbox-bubble inbox-focus-pulse" : "inbox-bubble"}
            data-mine={mine || undefined}
            data-incoming={!mine || undefined}
            data-first={first || undefined}
            data-last={last || undefined}
            data-original={original || undefined}
            data-public={publicUpdate || undefined}
            data-corrected={corrected || undefined}
            style={{ minWidth: 0 }}
          >
            {correction &&
              (correction.onJump ? (
                <UnstyledButton className="inbox-correction-ref" data-clickable onClick={correction.onJump}>
                  <Text size="xs" fw={600} c="deep-orange">
                    {t("inbox.correction_of")}
                  </Text>
                  {correction.text && (
                    <Text size="xs" c="dimmed" lineClamp={2}>
                      {correction.text}
                    </Text>
                  )}
                </UnstyledButton>
              ) : (
                <Box className="inbox-correction-ref">
                  <Text size="xs" fw={600} c="deep-orange">
                    {t("inbox.correction_of")}
                  </Text>
                  {correction.text && (
                    <Text size="xs" c="dimmed" lineClamp={2}>
                      {correction.text}
                    </Text>
                  )}
                </Box>
              ))}
            {quote && (
              <Box
                mb={6}
                pl={8}
                py={2}
                style={{
                  borderLeft: "2px solid var(--mantine-color-bright-blue-6)",
                  opacity: 0.85,
                }}
              >
                <Text size="xs" fw={600} truncate>
                  {quote.author}
                </Text>
                <Text size="xs" c="dimmed" lineClamp={2}>
                  {quote.text}
                </Text>
              </Box>
            )}
            {text && <MessageText text={text} highlight={highlight} mentions={mentions} />}
            <MessageAttachments conversationId={conversationId} attachments={attachments} hasText={Boolean(text)} onPreview={onPreview} />
            {expandedTranslation && (
              <Box mt={6} pt={6} className="inbox-sent-translation">
                <Text size="xs" c="dimmed">
                  {t("inbox.sent_translation", { lang: languageName(expandedTranslation.language, ui) })}
                </Text>
                <MessageText text={expandedTranslation.text} highlight={highlight} mentions={mentions} />
              </Box>
            )}
          </Paper>

          {(!first || copyText || extraActions) && (
            <Group gap={4} wrap="nowrap" className="inbox-msg-actions" data-mine={mine || undefined}>
              {!first && (
                <Tooltip label={fullTime} openDelay={300}>
                  <Text fz={11} c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                    {time}
                  </Text>
                </Tooltip>
              )}
              {!first && edited && (
                <Text fz={11} c="dimmed">
                  {t("inbox.message_edited")}
                </Text>
              )}
              {extraActions}
              {copyText && <CopyAction value={copyText} />}
            </Group>
          )}
        </Box>
      </Group>

      {(caption || correctedFooter || footerExtra) && (
        <Group
          gap={5}
          mt={4}
          pl={mine ? 0 : 42}
          pr={mine ? 42 : 0}
          justify={mine ? "flex-end" : "flex-start"}
          wrap="wrap"
          style={{ maxWidth: "100%" }}
        >
          {correctedFooter}
          {correctedFooter && (caption || footerExtra) && (
            <Text fz={11} c="dimmed">
              ·
            </Text>
          )}
          {caption}
          {caption && footerExtra && (
            <Text fz={11} c="dimmed">
              ·
            </Text>
          )}
          {footerExtra}
        </Group>
      )}
    </Box>
  )
}

/**
 * TimelineNote 是服务型会话与外部商务沟通共用的内部备注卡片：
 * 占满一行、淡黄底加左侧强调条、锁图标与作者头像，支持 @同事 高亮、网址链接、搜索词高亮、附件预览及跨语言译文切换。
 */
export function TimelineNote({
  conversationId,
  it,
  author,
  avatar,
  isOwn,
  time,
  fullTime,
  mt,
  lang,
  translating,
  watch,
  highlight,
  focused,
  onPreview,
}: {
  conversationId: string
  it: ConversationItemDTO
  author: string
  avatar?: ReactNode
  isOwn: boolean
  time: string
  fullTime: string
  mt: number
  lang: string | undefined
  translating: boolean
  watch: FillWatcher
  highlight?: string
  focused?: boolean
  onPreview: (image: LightboxImage) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const queryClient = useQueryClient()
  const [showOriginal, setShowOriginal] = useState(false)
  const [showVerified, setShowVerified] = useState(false)
  const [editing, setEditing] = useState(false)
  const [editText, setEditText] = useState("")
  const [recalling, setRecalling] = useState(false)

  const edit = useMutation({
    mutationFn: (text: string) => tenantApi.conversation.itemEdit(conversationId, it.id, { text }),
    onSuccess: () => {
      setEditing(false)
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
    },
    onError: (err) => notifyError(err),
  })
  const recall = useMutation({
    mutationFn: () => tenantApi.conversation.itemRecall(conversationId, it.id),
    onSuccess: () => {
      setRecalling(false)
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
    },
    onError: (err) => notifyError(err),
  })

  const recalled = Boolean(it.recalled)
  const raw = it.content.text ?? ""
  const translated = !recalled && !isOwn ? translationOf(it, lang) : undefined
  const verified = !recalled && isOwn ? verifiedTranslationOf(it) : undefined
  const shown = recalled ? "" : translated && !showOriginal ? translated : raw
  const fromLang = it.derived.language as string | undefined
  const needFill = !recalled && !isOwn && translating && it.untranslated

  const caption = (
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
  )

  return (
    <>
      <Paper
        ref={needFill ? watch(it.id) : undefined}
        mt={mt}
        px="sm"
        py={8}
        radius="md"
        bg="var(--mantine-color-yellow-light)"
        data-seq={it.seq}
        className={focused ? "inbox-note inbox-focus-pulse" : "inbox-note"}
      >
        <Group gap={6} mb={4} wrap="nowrap">
          <IconLock size={12} className="inbox-note-accent" style={{ flex: "none" }} />
          {avatar}
          <Text size="xs" fw={600} className="inbox-note-accent">
            {t("inbox.note_by", { who: author })}
          </Text>
          <Tooltip label={fullTime} openDelay={300}>
            <Text fz={11} className="inbox-note-accent" opacity={0.8} style={{ fontVariantNumeric: "tabular-nums" }}>
              {time}
            </Text>
          </Tooltip>
          {Boolean(it.revision) && !recalled && (
            <Text fz={11} c="dimmed">
              · {t("inbox.message_edited")}
            </Text>
          )}
          {!recalled && (shown || isOwn) && (
            <Group gap={2} ml="auto" wrap="nowrap" className="inbox-note-actions">
              {isOwn && (
                <>
                  <Tooltip label={t("inbox.message_edit")} openDelay={250}>
                    <ActionIcon
                      variant="subtle"
                      color="gray"
                      size={20}
                      aria-label={t("inbox.message_edit")}
                      onClick={() => {
                        setEditText(raw)
                        setEditing(true)
                      }}
                    >
                      <IconPencil size={12} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label={t("inbox.message_recall")} openDelay={250}>
                    <ActionIcon
                      variant="subtle"
                      color="gray"
                      size={20}
                      aria-label={t("inbox.message_recall")}
                      onClick={() => setRecalling(true)}
                    >
                      <IconArrowBackUp size={12} />
                    </ActionIcon>
                  </Tooltip>
                </>
              )}
              {shown && <CopyAction value={shown} />}
            </Group>
          )}
        </Group>

        {recalled ? (
          <Text size="sm" c="dimmed" fs="italic">
            {t("inbox.note_recalled")}
          </Text>
        ) : (
          <>
            {shown && <MessageText text={shown} highlight={highlight} mentions mentionClassName="inbox-note-accent" />}
            <MessageAttachments conversationId={conversationId} attachments={it.attachments} hasText={Boolean(shown)} onPreview={onPreview} />

            {verified && showVerified && (
              <Box mt={6} pt={6} className="inbox-sent-translation">
                <Text size="xs" c="dimmed">
                  {t("inbox.sent_translation", { lang: languageName(verified.language, ui) })}
                </Text>
                <MessageText text={verified.text} highlight={highlight} mentions mentionClassName="inbox-note-accent" />
              </Box>
            )}

            {(translated || needFill || verified) && (
              <Group gap={4} mt={4} wrap="wrap">
                {caption}
              </Group>
            )}
          </>
        )}
      </Paper>
      <Modal opened={editing} onClose={() => setEditing(false)} title={t("inbox.note_edit_title")} centered>
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
      <Modal opened={recalling} onClose={() => setRecalling(false)} title={t("inbox.note_recall_title")} centered>
        <Text size="sm">{t("inbox.note_recall_confirm")}</Text>
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

/** TypingIndicator 渲染时间线底部的三点跳动“正在输入”胶囊。 */
export function TypingIndicator({ names }: { names: string[] }): ReactNode {
  const t = useT()
  if (names.length === 0) return null
  return (
    <Box mt={10} className="inbox-typing">
      <span className="inbox-typing-dots" aria-hidden="true">
        <span />
        <span />
        <span />
      </span>
      <Text size="xs" c="dimmed">
        {t("inbox.typing", { who: names.join(", ") })}
      </Text>
    </Box>
  )
}
